package forward

import (
	"fmt"
	"hash/fnv"
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
	// A tunnel on several lanes has one link per lane, its own first.
	ifByTunnel := map[string]string{}
	lanes := map[string][]string{}
	for _, l := range links {
		if _, ok := ifByTunnel[l.Tunnel]; ok {
			lanes[l.Tunnel] = append(lanes[l.Tunnel], l.Name)
		} else {
			ifByTunnel[l.Tunnel] = l.Name
		}
		rs.Ifaces = append(rs.Ifaces, l.Name)
	}
	ts, _ := st.Effective()
	byName := map[string]config.Tunnel{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	for _, f := range fs {
		t, ok := byName[f.Tunnel]
		if ok && f.ModeOf() == config.ForwardRelay {
			// Relay forwards don't use the tunnel's interface.
			specs, err := streamRelays(st, f, t, byName)
			if err != nil {
				errs = append(errs, fmt.Errorf("forward %s on %s: %w", f.Ports, f.Tunnel, err))
				continue
			}
			rs.Relays = append(rs.Relays, specs...)
			continue
		}
		if ok && f.ModeOf() == config.ForwardSocks {
			spec, err := socksRelay(st, f, t, byName)
			if err != nil {
				errs = append(errs, fmt.Errorf("proxy on port %s over %s: %w", f.Ports, f.Tunnel, err))
				continue
			}
			rs.Relays = append(rs.Relays, spec)
			continue
		}
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
		rs.Rules = append(rs.Rules, Rule{Iface: iface, Lanes: lanes[f.Tunnel], Proto: ps, Ports: ports, Target: target.Addr(), To: f.To,
			Transparent: f.ModeOf() == config.ForwardTransparent, Key: f.Proto + "/" + f.Ports})
	}
	for _, name := range st.ReturnPaths() {
		if iface, up := ifByTunnel[name]; up {
			rs.Returns = append(rs.Returns, Return{Iface: iface, Lanes: lanes[name], Mark: ReturnMark(name)})
		}
	}
	rs.Exits = relayExits(st, byName)
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
		fam, nat, target := r.dnat()
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
	if len(rs.Rules) > 0 {
		// Connections this server makes itself never pass prerouting: an
		// Xray outbound here to 127.0.0.1:port, or to one of this server's
		// own addresses, takes the same forwards on output. They are always
		// masqueraded, transparent or not, since the far end can't answer
		// a loopback address. IPv6 has no way out of ::1.
		b.WriteString("  chain output {\n    type nat hook output priority -100; policy accept;\n")
		for _, r := range rs.Rules {
			fam, nat, target := r.dnat()
			lo := ""
			if r.Target.Is6() {
				lo = "ip6 daddr != ::1 "
			}
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    meta nfproto %s %sfib daddr type local %s dport { %s } dnat %s to %s\n",
					fam, lo, p, r.Ports, nat, target)
			}
		}
		b.WriteString("  }\n")
	}
	if localnet(rs) {
		// Tunnels whose forwards serve 127.0.0.1 accept packets for the
		// loopback network (route_localnet, see Apply), so the replies get
		// back. Before NAT those replies are addressed to the tunnel's own
		// address; anything sent to 127.0.0.0/8 through a tunnel is forged.
		b.WriteString("  chain localnet {\n    type filter hook prerouting priority raw; policy accept;\n")
		b.WriteString("    iifname \"nt-*\" ip daddr 127.0.0.0/8 drop\n")
		b.WriteString("  }\n")
	}
	b.WriteString("  chain postrouting {\n    type nat hook postrouting priority srcnat; policy accept;\n")
	seen := map[string]bool{}
	for _, r := range rs.Rules {
		if seen[r.Iface] {
			continue
		}
		seen[r.Iface] = true
		if transparent[r.Iface] {
			// Transparent forwards keep the client's address.
			fmt.Fprintf(&b, "    oifname %s ct status dnat ct mark != 0x%08x masquerade\n", ifMatch(r.Iface, r.Lanes), transparentMark)
		} else {
			fmt.Fprintf(&b, "    oifname %s ct status dnat masquerade\n", ifMatch(r.Iface, r.Lanes))
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
	// Per-forward byte counters, in the client's own bytes (before the
	// tunnel wraps them): upload is counted on the way in, download on the
	// way out, each by the forward's public port. The daemon reads these.
	if names := counterNames(rs.Rules); len(names) > 0 {
		for _, n := range names {
			fmt.Fprintf(&b, "  counter %s { }\n  counter %s { }\n", n+"u", n+"d")
		}
		b.WriteString("  chain account_in {\n    type filter hook prerouting priority -150; policy accept;\n")
		for _, r := range rs.Rules {
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    iifname != \"nt-*\" %s dport { %s } counter name %su\n", p, r.Ports, counterName(r.Key))
			}
		}
		b.WriteString("  }\n")
		b.WriteString("  chain account_out {\n    type filter hook postrouting priority 110; policy accept;\n")
		for _, r := range rs.Rules {
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    oifname != \"nt-*\" %s sport { %s } counter name %sd\n", p, r.Ports, counterName(r.Key))
			}
		}
		b.WriteString("  }\n")
		// The same for this server's own connections: counted on output
		// before NAT, and their replies on input once un-NATed.
		b.WriteString("  chain account_local_out {\n    type filter hook output priority -150; policy accept;\n")
		for _, r := range rs.Rules {
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    fib daddr type local %s dport { %s } counter name %su\n", p, r.Ports, counterName(r.Key))
			}
		}
		b.WriteString("  }\n")
		b.WriteString("  chain account_local_in {\n    type filter hook input priority 110; policy accept;\n")
		for _, r := range rs.Rules {
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    iifname %s ct status dnat %s sport { %s } counter name %sd\n", ifMatch(r.Iface, r.Lanes), p, r.Ports, counterName(r.Key))
			}
		}
		b.WriteString("  }\n")
	}
	if len(rs.Returns) > 0 {
		// Return paths: connections that arrive through the tunnel from
		// clients elsewhere are marked, and so is every packet of them (TCP
		// sockets inherit the mark of the SYN); routing rules send marked
		// packets that would take the default route back through the tunnel.
		b.WriteString("  chain return_in {\n    type filter hook prerouting priority mangle; policy accept;\n")
		for _, r := range rs.Returns {
			fmt.Fprintf(&b, "    iifname %s ct state new ct mark set 0x%08x\n", ifMatch(r.Iface, r.Lanes), r.Mark)
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

// counterName is the nftables named-counter prefix for a forward key; "u"
// and "d" are appended for the upload and download directions.
func counterName(key string) string {
	h := fnv.New32a()
	h.Write([]byte(key))
	return fmt.Sprintf("nt%08x", h.Sum32())
}

// counterNames returns the unique counter prefixes of a ruleset, in order.
func counterNames(rules []Rule) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rules {
		if r.Key == "" || seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		out = append(out, counterName(r.Key))
	}
	return out
}

// CounterName exposes the counter prefix so the daemon can read a forward's
// bytes: CounterName(proto, ports)+"u" and +"d".
func CounterName(proto, ports string) string { return counterName(proto + "/" + ports) }
