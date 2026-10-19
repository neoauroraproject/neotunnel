package forward

import (
	"errors"
	"fmt"
	"net/netip"

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
	exit, cipher, err := relayPeer(st, t, byName)
	if err != nil {
		return nil, err
	}
	ports := Expand(f.Ports)
	if len(ports) > MaxRelayPorts {
		return nil, fmt.Errorf("relay mode supports up to %d ports per forward", MaxRelayPorts)
	}
	via := relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Exit: netip.AddrPortFrom(exit, relay.RelayPort), Cipher: cipher, Pool: poolSize(t)}
	for _, n := range []string{t.Net4, t.Net6} {
		if far, ok := config.Inner(n, 1-me); ok && n != "" {
			via.Inside = netip.AddrPortFrom(far.Addr(), relay.RelayPort)
			break
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
	via := relay.Via{Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Cipher: tunnelCipher(t, byName), Pool: poolSize(t)}
	for _, n := range []string{t.Net4, t.Net6} {
		if far, ok := config.Inner(n, 1-me); ok && n != "" {
			via.Exit = netip.AddrPortFrom(far.Addr(), relay.RelayPort)
			break
		}
	}
	if !via.Exit.IsValid() {
		return relay.Spec{}, errors.New("the tunnel has no inner address")
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
func farAddrs(st *config.State, t config.Tunnel) []netip.Addr {
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
	return out
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

// relayExits lists the tunnels whose relay forwards end on this node: their
// far node's forwards, with the ports the relay may connect to here.
func relayExits(st *config.State, byName map[string]config.Tunnel) []relay.Exit {
	fs := st.AllForwards()
	var out []relay.Exit
	for _, f := range fs {
		if (f.ModeOf() != config.ForwardRelay && f.ModeOf() != config.ForwardSocks) || st.IsSelf(f.Node) {
			continue
		}
		// The far side may move the forward to its backup at any time, so
		// the exits of both tunnels are open.
		for _, name := range []string{f.Tunnel, f.Backup} {
			t, ok := byName[name]
			if name == "" || !ok || t.Disabled || t.Secret == "" || t.Sides[1-sideOf(st, t)].Node != f.Node {
				continue
			}
			e := relay.Exit{Tunnel: t.KeyName(), Secret: t.Secret, Side: sideOf(st, t), From: farAddrs(st, t)}
			if f.ModeOf() == config.ForwardSocks {
				e.Egress = true
			} else if f.To != 0 {
				e.Ports = []uint16{f.To}
			} else {
				for _, p := range Expand(f.Ports) {
					e.Ports = append(e.Ports, uint16(p))
				}
			}
			out = append(out, e)
		}
	}
	return out
}
