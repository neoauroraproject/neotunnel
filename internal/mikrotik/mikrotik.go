// Package mikrotik writes the RouterOS v7 commands for the far end of a
// tunnel that ends on a MikroTik router instead of a NeoTunnel server, so
// the user can paste them into the router's terminal.
package mikrotik

import (
	"fmt"
	"net/netip"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
)

// Commands returns the RouterOS script for the router end of tunnel name:
// every layer of its stack (bottom first), the inner addresses, and notes
// on the ports forwarded to the router.
func Commands(st *config.State, name string) (string, error) {
	ts, fs := st.Effective()
	byName := map[string]config.Tunnel{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	top, ok := byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tunnel %q", name)
	}
	// The stack, bottom first.
	var stack []config.Tunnel
	for t, depth := top, 0; ; depth++ {
		if depth > 4 {
			return "", fmt.Errorf("tunnel %s: 'over' chain too deep or circular", name)
		}
		stack = append([]config.Tunnel{t}, stack...)
		if t.Over == "" {
			break
		}
		over := t.Over
		if t, ok = byName[over]; !ok {
			return "", fmt.Errorf("tunnel %s: carrier %s not found", name, over)
		}
	}
	bottom := stack[0]
	me := 0
	if st.IsSelf(bottom.Sides[1].Node) {
		me = 1
	}
	far := bottom.Sides[1-me]
	if far.Node != "" {
		return "", fmt.Errorf("the far end of %s is the NeoTunnel server %s, which configures itself; MikroTik commands are for a router at a remote IP", name, far.Node)
	}
	server := bottom.Sides[me].Endpoint
	if server == "" {
		server = bottom.Sides[me].Local
	}
	serverIP, err := netip.ParseAddr(server)
	if err != nil {
		return "", fmt.Errorf("this server's public address is unknown; set it first (neotunnel node set --address <ip>)")
	}
	serverIP = serverIP.Unmap()

	// Render this end to learn what the router must match: MTU, keys, ports.
	resolve := func(remote netip.Addr) (netip.Addr, error) {
		if remote.Is4() == serverIP.Is4() {
			return serverIP, nil
		}
		if remote.Is4() {
			return netip.IPv4Unspecified(), nil
		}
		return netip.IPv6Unspecified(), nil
	}
	specs, errs := engine.RenderTunnels(st.IsSelf, stack, resolve)
	if len(errs) > 0 {
		return "", errs[0]
	}
	spec := map[string]engine.LinkSpec{}
	for _, s := range specs {
		if s.Lane == 0 {
			spec[s.Tunnel] = s
		}
	}

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	title := string(top.Kind)
	if top.Mode != "" {
		title = top.Mode
	}
	w("# NeoTunnel %s (%s): RouterOS v7 commands for the router at %s", name, title, far.Endpoint)
	w("# NeoTunnel server: %s", serverIP)

	for _, t := range stack {
		s := spec[t.Name]
		ifname := engine.Ifname(t.Name)
		comment := fmt.Sprintf("comment=\"NeoTunnel %s\"", t.Name)
		// The address the router dials, and its own address for a layer
		// over another tunnel (both from the carrier's inner subnet).
		remote, local := serverIP.String(), ""
		if t.Over != "" {
			// The spec is this server's view: its Local is our remote.
			remote, local = s.Local.String(), s.Remote.String()
		}
		localArg := ""
		if local != "" {
			localArg = " local-address=" + local
		}
		dscp := ""
		if t.DSCP != 0 {
			dscp = fmt.Sprintf(" dscp=%d", t.DSCP)
		}
		if s.FOU {
			return "", fmt.Errorf("tunnel %s runs on UDP port %d (FOU), which RouterOS does not support; remove the port", t.Name, s.Port)
		}
		under6 := s.Remote.Is6()
		switch t.Kind {
		case config.KindSIT:
			w("/interface 6to4 add name=%s remote-address=%s%s mtu=%d%s %s", ifname, remote, localArg, s.MTU, dscp, comment)
		case config.KindIPIP:
			w("/interface ipip add name=%s remote-address=%s%s mtu=%d%s %s", ifname, remote, localArg, s.MTU, dscp, comment)
		case config.KindGRE:
			w("/interface gre add name=%s remote-address=%s%s mtu=%d%s %s", ifname, remote, localArg, s.MTU, dscp, comment)
		case config.KindIP6GRE:
			w("/interface gre6 add name=%s remote-address=%s%s mtu=%d%s %s", ifname, remote, localArg, s.MTU, dscp, comment)
		case config.KindIPIP6, config.KindIP6IP6:
			w("/interface ipipv6 add name=%s remote-address=%s%s mtu=%d%s %s", ifname, remote, localArg, s.MTU, dscp, comment)
		case config.KindEoIP:
			if s.Port != 0 {
				return "", fmt.Errorf("tunnel %s runs EoIP on UDP port %d, which only NeoTunnel speaks; remove the port for a MikroTik", t.Name, s.Port)
			}
			menu := "eoip"
			if under6 {
				menu = "eoipv6"
			}
			w("/interface %s add name=%s remote-address=%s%s tunnel-id=%d mtu=%d%s %s", menu, ifname, remote, localArg, s.Key, s.MTU, dscp, comment)
		case config.KindVXLAN:
			// RouterOS defaults to port 8472, so the port is always given.
			w("/interface vxlan add name=%s vni=%d port=%d mtu=%d %s", ifname, s.VNI, s.Port, s.MTU, comment)
			w("/interface vxlan vteps add interface=%s remote-ip=%s", ifname, remote)
		case config.KindL2TPv3:
			// Static (unmanaged) L2TPv3: both ends use one id for the
			// tunnel and the session, as NeoTunnel does.
			id := s.Key & 0x7fffffff
			if id == 0 {
				id = 1
			}
			proto := "l2tpv3-ip"
			if s.Port != 0 {
				proto = "l2tpv3-udp"
				if s.Port != 1701 {
					w("# RouterOS sends L2TPv3 over UDP on port 1701: give the tunnel port 1701 in NeoTunnel")
				}
			}
			own := far.Endpoint
			if local != "" {
				own = local
			}
			w("# local-address is the router's own address; behind NAT, use the address on its WAN interface")
			w("/interface l2tp-ether add name=%s connect-to=%s local-address=%s unmanaged-mode=yes l2tp-proto-version=%s local-tunnel-id=%d remote-tunnel-id=%d local-session-id=%d remote-session-id=%d mtu=%d disabled=no %s",
				ifname, remote, own, proto, id, id, id, id, s.MTU, comment)
		case config.KindWireGuard:
			pub, err := engine.WireGuardPublicKey(t, me)
			if err != nil {
				return "", err
			}
			ka := ""
			switch {
			case t.Keepalive == 0:
				ka = " persistent-keepalive=25s"
			case t.Keepalive > 0:
				ka = fmt.Sprintf(" persistent-keepalive=%ds", t.Keepalive)
			}
			var allowed []string
			for _, n := range []string{t.Net4, t.Net6} {
				if p, ok := config.Inner(n, me); ok {
					allowed = append(allowed, netip.PrefixFrom(p.Addr(), p.Addr().BitLen()).String())
				}
			}
			// The router generates its own private key when the interface
			// is created; NeoTunnel needs the matching public key.
			w("/interface wireguard add name=%s listen-port=%d mtu=%d %s", ifname, s.Port, s.MTU, comment)
			w("/interface wireguard peers add interface=%s public-key=\"%s\" endpoint-address=%s endpoint-port=%d allowed-address=%s%s %s",
				ifname, pub, remote, s.Port, strings.Join(allowed, ","), ka, comment)
			if t.PeerKey != "" {
				w("# NeoTunnel expects the router's public key %s;", t.PeerKey)
				w("# if the router made a different one, give NeoTunnel this key as the peer key:")
			} else {
				w("# give NeoTunnel the router's public key (peer key):")
			}
			w(":put [/interface wireguard get %s public-key]", ifname)
		case config.KindGRETAP, config.KindIP6GRETAP:
			return "", fmt.Errorf("RouterOS has no GRETAP; for an Ethernet tunnel to a MikroTik use EoIP, VXLAN or L2TPv3")
		default:
			return "", fmt.Errorf("RouterOS has no %s tunnel; to a MikroTik use GRE, IPIP, 6to4, EoIP, VXLAN, L2TPv3 or WireGuard", t.Kind)
		}
	}

	for _, t := range stack {
		ifname := engine.Ifname(t.Name)
		if p, ok := config.Inner(t.Net4, 1-me); ok {
			w("/ip address add address=%s interface=%s comment=\"NeoTunnel %s\"", p, ifname, t.Name)
		}
		if p, ok := config.Inner(t.Net6, 1-me); ok {
			w("/ipv6 address add address=%s interface=%s advertise=no comment=\"NeoTunnel %s\"", p, ifname, t.Name)
		}
	}

	// Forwarded ports reach the router with this server's tunnel address
	// as the source, which is on the tunnel's connected subnet: replies
	// need no route. Only nat and proxy modes work toward a router.
	var forwards []config.Forward
	for _, f := range fs {
		if f.Tunnel == name {
			forwards = append(forwards, f)
		}
	}
	if len(forwards) > 0 {
		here, there := innerPair(top, me)
		w("# Ports forwarded here arrive on %s from %s (connected, no route needed):", there, here)
		for _, f := range forwards {
			to := "same port"
			if f.To != 0 {
				to = fmt.Sprintf("port %d", f.To)
			}
			extra := ""
			if f.ModeOf() == config.ForwardProxy {
				extra = fmt.Sprintf(", with a PROXY protocol v%d header", proxyVersion(f))
			}
			w("#   %s %s -> %s%s", f.Proto, f.Ports, to, extra)
		}
		w("# to pass them on to a host behind the router, e.g.:")
		w("# /ip firewall nat add chain=dstnat in-interface=%s protocol=tcp dst-port=%s action=dst-nat to-addresses=192.168.88.10", engine.Ifname(name), forwards[0].Ports)
	}
	return b.String(), nil
}

// innerPair returns this server's and the router's inner address of t,
// IPv4 when it has one.
func innerPair(t config.Tunnel, me int) (string, string) {
	n := t.Net4
	if n == "" {
		n = t.Net6
	}
	a, _ := config.Inner(n, me)
	c, _ := config.Inner(n, 1-me)
	return a.Addr().String(), c.Addr().String()
}

func proxyVersion(f config.Forward) int {
	if f.ProxyVersion == 0 {
		return 2
	}
	return f.ProxyVersion
}
