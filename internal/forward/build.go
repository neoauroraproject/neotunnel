package forward

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/relay"
)

// Link is a running tunnel interface.
type Link struct {
	Name   string
	Tunnel string
}

// Build resolves forwards to concrete targets (the far end's inner address):
// nft DNAT rules for nat and transparent forwards, relays for proxy ones, and
// the return paths for transparent forwards arriving from the far side.
func Build(st *config.State, fs []config.Forward, links []Link) (Ruleset, []error) {
	var rs Ruleset
	var errs []error
	ifByTunnel := map[string]string{}
	for _, l := range links {
		ifByTunnel[l.Tunnel] = l.Name
		rs.Ifaces = append(rs.Ifaces, l.Name)
	}
	ts, _ := st.Effective()
	byName := map[string]config.Tunnel{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	for _, f := range fs {
		t, ok := byName[f.Tunnel]
		iface, up := ifByTunnel[f.Tunnel]
		if !ok || !up {
			errs = append(errs, fmt.Errorf("forward %s: tunnel %s is not running here", f.Ports, f.Tunnel))
			continue
		}
		me := 0
		if st.IsSelf(t.Sides[1].Node) {
			me = 1
		}
		net := t.Net4
		if net == "" {
			net = t.Net6
		}
		target, ok := config.Inner(net, 1-me)
		if !ok {
			errs = append(errs, fmt.Errorf("forward %s: tunnel %s has no inner address", f.Ports, f.Tunnel))
			continue
		}
		ports, err := ParsePorts(f.Ports)
		if err != nil {
			errs = append(errs, fmt.Errorf("forward on %s: %w", f.Tunnel, err))
			continue
		}
		ps, err := protos(f.Proto)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if f.ModeOf() == config.ForwardProxy {
			specs, err := relays(st, f, ps, target.Addr())
			if err != nil {
				errs = append(errs, fmt.Errorf("forward %s on %s: %w", f.Ports, f.Tunnel, err))
				continue
			}
			rs.Relays = append(rs.Relays, specs...)
			continue
		}
		rs.Rules = append(rs.Rules, Rule{Iface: iface, Proto: ps, Ports: ports, Target: target.Addr(), To: f.To,
			Transparent: f.ModeOf() == config.ForwardTransparent})
	}
	for _, name := range st.ReturnPaths() {
		if iface, up := ifByTunnel[name]; up {
			rs.Returns = append(rs.Returns, Return{Iface: iface, Mark: ReturnMark(name)})
		}
	}
	return rs, errs
}

// relays lists the listeners of a proxy-mode forward, one per port and
// protocol.
func relays(st *config.State, f config.Forward, ps []string, target netip.Addr) ([]relay.Spec, error) {
	version := f.ProxyVersion
	if version == 0 {
		version = 2
	}
	public, _ := netip.ParseAddr(st.Address)
	ports := Expand(f.Ports)
	if len(ports) > MaxRelayPorts {
		return nil, fmt.Errorf("proxy mode supports up to %d ports per forward", MaxRelayPorts)
	}
	var out []relay.Spec
	for _, p := range ps {
		if p == "udp" && version == 1 {
			return nil, fmt.Errorf("PROXY protocol v1 has no UDP form; use v2")
		}
		for _, port := range ports {
			to := uint16(port)
			if f.To != 0 {
				to = f.To
			}
			out = append(out, relay.Spec{Proto: p, Port: uint16(port), Target: netip.AddrPortFrom(target, to),
				Version: version, Public: public})
		}
	}
	return out, nil
}

// Expand lists every port in "443,8080-8090" (invalid parts are skipped).
func Expand(ports string) []int {
	var out []int
	for _, f := range strings.Split(ports, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(f), "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		for p := a; p <= b && p <= 65535; p++ {
			out = append(out, p)
		}
	}
	return out
}

// Script renders the full nft transaction. An empty ruleset just removes the table.
func Script(rs Ruleset) string {
	var b strings.Builder
	// Creating then deleting makes the delete safe when the table doesn't exist.
	b.WriteString("table inet neotunnel\ndelete table inet neotunnel\n")
	if len(rs.Rules) == 0 && len(rs.Ifaces) == 0 && len(rs.Returns) == 0 {
		return b.String()
	}
	b.WriteString("table inet neotunnel {\n")
	b.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
	transparent := map[string]bool{}
	for _, r := range rs.Rules {
		fam, nat := "ipv4", "ip"
		target := r.Target.String()
		if r.Target.Is6() {
			fam, nat, target = "ipv6", "ip6", "["+target+"]"
		}
		if r.To != 0 {
			target += ":" + strconv.Itoa(int(r.To))
		}
		mark := ""
		if r.Transparent {
			transparent[r.Iface] = true
			mark = fmt.Sprintf("ct mark set 0x%08x ", transparentMark)
		}
		for _, p := range r.Proto {
			fmt.Fprintf(&b, "    meta nfproto %s iifname != \"nt-*\" fib daddr type local %s dport { %s } %sdnat %s to %s\n",
				fam, p, r.Ports, mark, nat, target)
		}
	}
	b.WriteString("  }\n")
	b.WriteString("  chain postrouting {\n    type nat hook postrouting priority srcnat; policy accept;\n")
	seen := map[string]bool{}
	for _, r := range rs.Rules {
		if seen[r.Iface] {
			continue
		}
		seen[r.Iface] = true
		if transparent[r.Iface] {
			// Transparent forwards keep the client's address.
			fmt.Fprintf(&b, "    oifname %q ct status dnat ct mark != 0x%08x masquerade\n", r.Iface, transparentMark)
		} else {
			fmt.Fprintf(&b, "    oifname %q ct status dnat masquerade\n", r.Iface)
		}
	}
	b.WriteString("  }\n")
	// Clamp TCP MSS to the path MTU so traffic through tunnels never stalls
	// on oversized segments.
	b.WriteString("  chain forward {\n    type filter hook forward priority mangle; policy accept;\n")
	// Flag syntax kept compatible with nftables 0.9 (Ubuntu 20.04).
	b.WriteString("    oifname \"nt-*\" tcp flags & (syn|rst) == syn tcp option maxseg size set rt mtu\n")
	b.WriteString("    iifname \"nt-*\" tcp flags & (syn|rst) == syn tcp option maxseg size set rt mtu\n")
	b.WriteString("  }\n")
	if len(rs.Returns) > 0 {
		// Return paths: connections that arrive through the tunnel from
		// clients elsewhere are marked, and so is every packet of them (TCP
		// sockets inherit the mark of the SYN); routing rules send marked
		// packets that would take the default route back through the tunnel.
		b.WriteString("  chain return_in {\n    type filter hook prerouting priority mangle; policy accept;\n")
		for _, r := range rs.Returns {
			fmt.Fprintf(&b, "    iifname %q ct state new ct mark set 0x%08x\n", r.Iface, r.Mark)
			fmt.Fprintf(&b, "    ct mark 0x%08x meta mark set ct mark\n", r.Mark)
		}
		b.WriteString("  }\n")
		b.WriteString("  chain return_out {\n    type route hook output priority mangle; policy accept;\n")
		for _, r := range rs.Returns {
			fmt.Fprintf(&b, "    ct mark 0x%08x meta mark set ct mark\n", r.Mark)
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// ProtectScript renders the table that drops unencrypted packets of
// encrypted tunnels. It is loaded on its own so an nftables too old for it
// can't take the forwards down.
func ProtectScript(ps []Protect) string {
	var b strings.Builder
	b.WriteString("table inet neotunnel_ipsec\ndelete table inet neotunnel_ipsec\n")
	if len(ps) == 0 {
		return b.String()
	}
	b.WriteString("table inet neotunnel_ipsec {\n  chain input {\n    type filter hook input priority filter - 5; policy accept;\n")
	for _, p := range ps {
		fam := "ip"
		if p.Remote.Is6() {
			fam = "ip6"
		}
		if p.Port != 0 {
			fmt.Fprintf(&b, "    %s saddr %s udp dport %d meta secpath missing drop\n", fam, p.Remote, p.Port)
		} else {
			fmt.Fprintf(&b, "    %s saddr %s meta l4proto %d meta secpath missing drop\n", fam, p.Remote, p.Proto)
		}
	}
	b.WriteString("  }\n}\n")
	return b.String()
}
