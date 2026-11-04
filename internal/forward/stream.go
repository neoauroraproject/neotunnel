package forward

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/relay"
	"neotunnel/internal/tcptun"
)

// streamRelays lists the listeners of a relay-mode forward on its entry
// node: each carries its users to the tunnel's far node over TCP of its own.
func streamRelays(st *config.State, f config.Forward, t config.Tunnel, byName map[string]config.Tunnel) ([]relay.Spec, error) {
	if t.Secret == "" {
		return nil, errors.New("the tunnel has no secret for relay mode; save the forward again")
	}
	me := sideOf(st, t)
	ports := Expand(f.Ports)
	if len(ports) > MaxRelayPorts {
		return nil, fmt.Errorf("relay mode supports up to %d ports per forward", MaxRelayPorts)
	}
	var via relay.Via
	if t.IsStream() {
		// A connection tunnel carries each user on a stream of its sessions.
		via = relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Session: t.Name}
	} else {
		exit, cipher, err := relayPeer(st, t, byName)
		if err != nil {
			return nil, err
		}
		rp := t.RelayPortsOf()
		via = relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Exit: netip.AddrPortFrom(exit, rp[0]), Cipher: cipher, Pool: poolSize(t)}
		copy(via.Ports[:], t.RelayPorts) // none: just RelayPort, as before
		if far, ok := insideAddr(st, t, byName); ok {
			via.Inside = netip.AddrPortFrom(far, rp[0])
		}
	}
	var out []relay.Spec
	for _, port := range ports {
		to := uint16(port)
		if f.To != 0 {
			to = f.To
		}
		out = append(out, relay.Spec{Proto: "tcp", Port: uint16(port), Target: netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), to),
			Version: f.ProxyVersion, Via: via})
	}
	return out, nil
}

// socksRelay is the listener of a socks forward: a SOCKS5 and HTTP proxy
// whose connections go to the far node through the tunnel (its inner
// address) and leave to the internet from there.
func socksRelay(st *config.State, f config.Forward, t config.Tunnel, byName map[string]config.Tunnel) (relay.Spec, error) {
	if t.Secret == "" {
		return relay.Spec{}, errors.New("the tunnel has no secret for the proxy; save it again")
	}
	me := sideOf(st, t)
	var via relay.Via
	if t.IsStream() {
		via = relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Session: t.Name}
	} else {
		rp := t.RelayPortsOf()
		via = relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Cipher: tunnelCipher(t, byName), Pool: poolSize(t)}
		copy(via.Ports[:], t.RelayPorts) // none: just RelayPort, as before
		if far, ok := insideAddr(st, t, byName); ok {
			via.Exit = netip.AddrPortFrom(far, rp[0])
		}
		if !via.Exit.IsValid() {
			return relay.Spec{}, errors.New("the tunnel has no inner address")
		}
	}
	listen := netip.AddrFrom4([4]byte{127, 0, 0, 1})
	if f.Listen != "" {
		a, err := netip.ParseAddr(f.Listen)
		if err != nil {
			return relay.Spec{}, fmt.Errorf("bad listen address %q", f.Listen)
		}
		listen = a
	}
	ports := Expand(f.Ports)
	if len(ports) != 1 {
		return relay.Spec{}, errors.New("a proxy listens on one port")
	}
	return relay.Spec{Proto: "tcp", Port: uint16(ports[0]), Via: via, Socks: true, Listen: listen, Auth: f.Auth}, nil
}

// fetchRelay is the downloads listener (see config.Fetch): its connections
// to listed sites go through the tunnel like a socks forward's.
func fetchRelay(st *config.State, f config.Fetch, byName map[string]config.Tunnel) (relay.Spec, error) {
	t, ok := byName[f.Tunnel]
	if !ok {
		return relay.Spec{}, fmt.Errorf("tunnel %s is not running here", f.Tunnel)
	}
	sites := f.Sites()
	if len(sites) == 0 {
		return relay.Spec{}, errors.New("no sites are chosen")
	}
	s, err := socksRelay(st, config.Forward{Tunnel: t.Name, Ports: fmt.Sprint(config.FetchPort)}, t, byName)
	if err != nil {
		return relay.Spec{}, err
	}
	s.Socks = false
	s.FetchSites, s.FetchStrict = strings.Join(sites, ","), f.Strict
	return s, nil
}

// poolSize is how many relay connections a tunnel keeps ready.
func poolSize(t config.Tunnel) int {
	switch t.Profile {
	case config.ProfileLean:
		return 2
	case config.ProfileMax:
		return 8
	}
	return 4
}

// tunnelCipher is the cipher of a tunnel or the first encrypted one it runs
// over, for its relay connections ("" = authenticated only).
func tunnelCipher(t config.Tunnel, byName map[string]config.Tunnel) string {
	for cur, hops := t, 0; hops < 8; hops++ {
		if tcptun.ValidCipher(cur.Encrypt) == nil && cur.Encrypt != "" {
			return cur.Encrypt
		}
		next, ok := byName[cur.Over]
		if cur.Over == "" || !ok {
			break
		}
		cur = next
	}
	return ""
}

// farAddrs are the addresses a tunnel's far node connects from: its public
// one and its inner ones.
func farAddrs(st *config.State, t config.Tunnel, byName map[string]config.Tunnel) []netip.Addr {
	me := sideOf(st, t)
	var out []netip.Addr
	if a, err := netip.ParseAddr(t.Sides[1-me].Endpoint); err == nil {
		out = append(out, a.Unmap())
	}
	for _, n := range []string{t.Net4, t.Net6} {
		if p, ok := config.Inner(n, 1-me); ok && n != "" {
			out = append(out, p.Addr())
		}
	}
	if c, ok := sitCarrier(t, byName); ok {
		if p, ok := config.Inner(c.Net6, 1-sideOf(st, c)); ok {
			out = append(out, p.Addr())
		}
	}
	return out
}

// insideAddr is the far node's address inside the tunnel for relay and
// proxy connections. On 6to4 (IPv4 in IPv6 over a SIT carrier) it is the
// carrier's IPv6 address: the connections still travel as protocol 41, but
// inside one layer instead of two, which the kernel handles several times
// faster (it can't batch packets through two tunnel layers, on sending or
// receiving).
func insideAddr(st *config.State, t config.Tunnel, byName map[string]config.Tunnel) (netip.Addr, bool) {
	if c, ok := sitCarrier(t, byName); ok {
		if p, ok := config.Inner(c.Net6, 1-sideOf(st, c)); ok {
			return p.Addr(), true
		}
	}
	for _, n := range []string{t.Net4, t.Net6} {
		if far, ok := config.Inner(n, 1-sideOf(st, t)); ok && n != "" {
			return far.Addr(), true
		}
	}
	return netip.Addr{}, false
}

// sitCarrier is the SIT tunnel right under t, with its IPv6 network.
func sitCarrier(t config.Tunnel, byName map[string]config.Tunnel) (config.Tunnel, bool) {
	c, ok := byName[t.Over]
	if t.Over == "" || !ok || c.Kind != config.KindSIT || c.Net6 == "" {
		return config.Tunnel{}, false
	}
	return c, true
}

func sideOf(st *config.State, t config.Tunnel) int {
	if st.IsSelf(t.Sides[1].Node) {
		return 1
	}
	return 0
}

// relayPeer finds the far node's public address (from the tunnel, or the
// tunnel it runs over) and the cipher the relay uses: the tunnel's own, so
// an encrypted tunnel's forwards are encrypted too.
func relayPeer(st *config.State, t config.Tunnel, byName map[string]config.Tunnel) (netip.Addr, string, error) {
	cipher := ""
	for cur, hops := t, 0; hops < 8; hops++ {
		if cipher == "" && tcptun.ValidCipher(cur.Encrypt) == nil {
			cipher = cur.Encrypt
		}
		if ep := cur.Sides[1-sideOf(st, cur)].Endpoint; ep != "" {
			a, err := netip.ParseAddr(ep)
			if err != nil {
				return netip.Addr{}, "", fmt.Errorf("far end %q is not an IP address", ep)
			}
			return a.Unmap(), cipher, nil
		}
		next, ok := byName[cur.Over]
		if cur.Over == "" || !ok {
			break
		}
		cur = next
	}
	return netip.Addr{}, "", errors.New("the far end has no public address")
}

// ownAddr is this node's public address as tunnel t (or the tunnel it runs
// over) knows it.
func ownAddr(st *config.State, t config.Tunnel, byName map[string]config.Tunnel) (netip.Addr, error) {
	for cur, hops := t, 0; hops < 8; hops++ {
		if ep := cur.Sides[sideOf(st, cur)].Endpoint; ep != "" {
			a, err := netip.ParseAddr(ep)
			if err != nil {
				return netip.Addr{}, fmt.Errorf("this end %q is not an IP address", ep)
			}
			return a.Unmap(), nil
		}
		next, ok := byName[cur.Over]
		if cur.Over == "" || !ok {
			break
		}
		cur = next
	}
	return netip.Addr{}, errors.New("this end has no public address")
}

// relayExits lists the tunnels whose relay forwards end on this node: their
// far node's forwards, with the ports the relay may connect to here.
func relayExits(st *config.State, byName map[string]config.Tunnel) []relay.Exit {
	fs := st.AllForwards()
	var out []relay.Exit
	for _, f := range fs {
		if (f.ModeOf() != config.ForwardRelay && f.ModeOf() != config.ForwardSocks) || st.IsSelf(f.Node) {
			continue
		}
		// The far side may move the forward to a backup at any time, so the
		// exits of all its tunnels are open.
		for _, name := range f.Paths() {
			t, ok := byName[name]
			if !ok || t.Disabled || t.Secret == "" || t.Sides[1-sideOf(st, t)].Node != f.Node {
				continue
			}
			e := relay.Exit{Tunnel: t.KeyName(), Secret: t.Secret, Side: sideOf(st, t), From: farAddrs(st, t, byName),
				Listen: t.RelayPorts, Session: t.IsStream()}
			if f.ModeOf() == config.ForwardSocks {
				e.Egress = true
			} else if f.To != 0 {
				e.Ports = []uint16{f.To}
			} else {
				for _, p := range Expand(f.Ports) {
					e.Ports = append(e.Ports, uint16(p))
				}
			}
			if f.Public && !e.Egress {
				// Delivered at this node's public address, not its loopback.
				if a, err := ownAddr(st, t, byName); err == nil {
					e.Public, e.Addr = e.Ports, a
				}
			}
			out = append(out, e)
		}
	}
	return out
}
