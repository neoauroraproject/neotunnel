package forward

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net/netip"
	"slices"
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
		if a, ok := account(f); ok {
			rs.Accounts = append(rs.Accounts, a)
			if p := f.Plan; p != nil {
				pl := Plan{Account: a, RateMbit: p.RateMbit, MaxConns: p.MaxConns, MaxIPs: p.MaxIPs, Closed: Closed(a.Key)}
				if pl.RateMbit > 0 || pl.MaxConns > 0 || pl.MaxIPs > 0 || pl.Closed != "" {
					rs.Plans = append(rs.Plans, pl)
				}
			}
		}
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
		dest, mark := target.Addr(), uint32(0)
		if f.Public {
			pub, err := publicTarget(st, f, t, byName, dest)
			if err != nil {
				errs = append(errs, fmt.Errorf("forward %s on %s: %w", f.Ports, f.Tunnel, err))
				continue
			}
			dest, mark = pub, PublicMark(t.Name)
			p := Public{Iface: iface, Lanes: lanes[f.Tunnel], Mark: mark, Dest: pub, Via: target.Addr()}
			if !slices.ContainsFunc(rs.Publics, func(x Public) bool { return x.Iface == p.Iface && x.Dest == p.Dest }) {
				rs.Publics = append(rs.Publics, p)
			}
		}
		if f.ModeOf() == config.ForwardProxy {
			specs, err := relays(st, f, ps, dest)
			if err != nil {
				errs = append(errs, fmt.Errorf("forward %s on %s: %w", f.Ports, f.Tunnel, err))
				continue
			}
			for i := range specs {
				specs[i].Mark = mark
			}
			rs.Relays = append(rs.Relays, specs...)
			continue
		}
		rs.Rules = append(rs.Rules, Rule{Iface: iface, Lanes: lanes[f.Tunnel], Proto: ps, Ports: ports, Target: dest, To: f.To,
			Transparent: f.ModeOf() == config.ForwardTransparent, Mark: mark, Key: f.Proto + "/" + f.Ports})
	}
	rs.Inner = innerNets(ts)
	for _, name := range st.ReturnPaths() {
		if iface, up := ifByTunnel[name]; up {
			rs.Returns = append(rs.Returns, Return{Iface: iface, Lanes: lanes[name], Mark: ReturnMark(name)})
		}
	}
	rs.Exits = relayExits(st, byName)
	if f, ok := st.FetchActive(); ok {
		spec, err := fetchRelay(st, f, byName)
		if err != nil {
			errs = append(errs, fmt.Errorf("downloads through %s: %w", f.Tunnel, err))
		} else {
			rs.Relays = append(rs.Relays, spec)
			rs.Fetch = &FetchRedirect{All: f.All}
		}
	}
	return rs, errs
}

// publicTarget is the far server's public address for f, a forward on t
// whose address inside the tunnel is inner.
func publicTarget(st *config.State, f config.Forward, t config.Tunnel, byName map[string]config.Tunnel, inner netip.Addr) (netip.Addr, error) {
	if f.ModeOf() == config.ForwardTransparent {
		return netip.Addr{}, errors.New("a transparent forward can't go to the far server's public address")
	}
	// These kinds route their outer packets with the inner packet's mark:
	// the mark that takes the forward into the tunnel would take the
	// tunnel's own packets there too.
	for cur, hops := t, 0; hops < 8; hops++ {
		if k := cur.Kind; k == config.KindVXLAN || k == config.KindGENEVE || k == config.KindL2TPv3 {
			return netip.Addr{}, fmt.Errorf("%s can't carry a forward to the far server's public address; use its address inside the tunnel", k)
		}
		next, ok := byName[cur.Over]
		if cur.Over == "" || !ok {
			break
		}
		cur = next
	}
	pub, _, err := relayPeer(st, t, byName)
	if err != nil {
		return netip.Addr{}, err
	}
	if pub.Is4() != inner.Is4() {
		return netip.Addr{}, fmt.Errorf("the far server's public address %s is not of the same family as its address inside the tunnel", pub)
	}
	return pub, nil
}

// innerNets are the subnets inside the tunnels, one of each, none within
// another (nft refuses overlapping elements).
func innerNets(ts []config.Tunnel) []netip.Prefix {
	var all []netip.Prefix
	for _, t := range ts {
		for _, n := range []string{t.Net4, t.Net6} {
			if p, err := netip.ParsePrefix(n); err == nil {
				all = append(all, p.Masked())
			}
		}
	}
	// Widest first, so anything inside one already kept is dropped.
	slices.SortFunc(all, func(a, b netip.Prefix) int {
		if a.Bits() != b.Bits() {
			return a.Bits() - b.Bits()
		}
		return a.Addr().Compare(b.Addr())
	})
	var out []netip.Prefix
	for _, p := range all {
		if !slices.ContainsFunc(out, func(q netip.Prefix) bool { return q.Contains(p.Addr()) && q.Bits() <= p.Bits() }) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return out
}

// account is what counts a forward's traffic; a downloads proxy has no
// clients of its own.
func account(f config.Forward) (Account, bool) {
	ports, err := ParsePorts(f.Ports)
	if err != nil || f.Fetch {
		return Account{}, false
	}
	ps, err := protos(f.Proto)
	if err != nil {
		return Account{}, false
	}
	return Account{Key: f.PortKey(), Proto: ps, Ports: ports}, true
}

// FetchScript renders the table that redirects this server's own HTTP and
// HTTPS connections to the downloads listener (config.Fetch). It is loaded
// on its own: the user.slice match needs a newer nftables and kernel than
// the rest, and a failure must not take the forwards down.
func FetchScript(fr *FetchRedirect) string {
	var b strings.Builder
	b.WriteString("table inet neotunnel_fetch\ndelete table inet neotunnel_fetch\n")
	if fr == nil {
		return b.String()
	}
	b.WriteString("table inet neotunnel_fetch {\n  chain output {\n    type nat hook output priority -110; policy accept;\n")
	// The daemon's own direct connections, the loopback, the tunnels and
	// this server's or private networks' addresses are never downloads.
	fmt.Fprintf(&b, "    meta mark 0x%08x return\n", relay.FetchMark)
	b.WriteString("    oifname { \"lo\", \"nt-*\" } return\n")
	b.WriteString("    fib daddr type local return\n")
	b.WriteString("    ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/3 } return\n")
	b.WriteString("    ip6 daddr { ::1, fc00::/7, fe80::/10, ff00::/8 } return\n")
	scope := ""
	if !fr.All {
		// Only what people run in a terminal or over SSH: their sessions
		// live under user.slice, services elsewhere.
		scope = "socket cgroupv2 level 1 \"user.slice\" "
	}
	fmt.Fprintf(&b, "    %stcp dport { 80, 443 } redirect to :%d\n", scope, config.FetchPort)
	b.WriteString("  }\n}\n")
	return b.String()
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
	if len(rs.Rules) == 0 && len(rs.Ifaces) == 0 && len(rs.Returns) == 0 && len(rs.Inner) == 0 {
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
		} else if r.Mark != 0 {
			mark = fmt.Sprintf("ct mark set 0x%08x ", r.Mark)
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
			mark := ""
			if r.Mark != 0 {
				mark = fmt.Sprintf("ct mark set 0x%08x ", r.Mark)
			}
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    meta nfproto %s %sfib daddr type local %s dport { %s } %sdnat %s to %s\n",
					fam, lo, p, r.Ports, mark, nat, target)
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
	// way out, each by the forward's public port, in every mode (relays and
	// proxies too). The daemon reads these.
	//
	// Every packet of the server passes these hooks, the tunnels' own
	// packets included, so each chain does one lookup per protocol (a map
	// from port to counter) after cheap checks that let everything else
	// through, instead of a rule per forward. What counts is the
	// connection's direction and the port it was opened to, not the
	// packet's ports: a connection this server opens itself from a port
	// that happens to be forwarded is no client's traffic.
	if names := counterNames(rs.Accounts); len(names) > 0 {
		for _, n := range names {
			fmt.Fprintf(&b, "  counter %s { }\n  counter %s { }\n", n+"u", n+"d")
		}
		// key gives the matches before the counter and what names the port.
		count := func(key func(proto string) (match, port string), suffix string) {
			for _, p := range []string{"tcp", "udp"} {
				if m := portMap(rs.Accounts, p, func(a Account) string { return "\"" + counterName(a.Key) + suffix + "\"" }); m != "" {
					match, port := key(p)
					fmt.Fprintf(&b, "    %scounter name %s map { %s }\n", match, port, m)
				}
			}
		}
		dport := func(p string) (string, string) { return "", p + " dport" }
		b.WriteString("  chain account_in {\n    type filter hook prerouting priority -150; policy accept;\n")
		b.WriteString("    iifname \"nt-*\" return\n    ct direction != original return\n")
		count(dport, "u")
		b.WriteString("  }\n")
		b.WriteString("  chain account_out {\n    type filter hook postrouting priority 110; policy accept;\n")
		b.WriteString("    oifname \"nt-*\" return\n    ct direction != reply return\n")
		count(func(p string) (string, string) { return "meta l4proto " + p + " ", "ct original proto-dst" }, "d")
		b.WriteString("  }\n")
		// The same for this server's own connections: counted on output
		// before NAT, and their replies on input once un-NATed.
		b.WriteString("  chain account_local_out {\n    type filter hook output priority -150; policy accept;\n")
		b.WriteString("    meta l4proto != { tcp, udp } return\n    fib daddr type != local return\n    ct direction != original return\n")
		count(dport, "u")
		b.WriteString("  }\n")
		b.WriteString("  chain account_local_in {\n    type filter hook input priority 110; policy accept;\n")
		b.WriteString("    iifname != \"nt-*\" return\n")
		for _, r := range rs.Rules {
			for _, p := range r.Proto {
				fmt.Fprintf(&b, "    iifname %s ct status dnat %s sport { %s } counter name %sd\n", ifMatch(r.Iface, r.Lanes), p, r.Ports, counterName(r.Key))
			}
		}
		b.WriteString("  }\n")
	}
	if len(rs.Publics) > 0 {
		// Forwards to the far server's public address: every packet of their
		// connections takes the mark the DNAT rule gave the connection, after
		// that rule (priority -99), so the routing rule of the mark sends it
		// through the tunnel; the tunnel's own packets to that address are
		// never marked. Proxy mode marks its sockets instead.
		b.WriteString("  chain public_in {\n    type filter hook prerouting priority -99; policy accept;\n")
		fmt.Fprintf(&b, "    ct direction original ct mark & 0xffff0000 == 0x%08x meta mark set ct mark\n", publicMarks)
		b.WriteString("  }\n")
		b.WriteString("  chain public_out {\n    type route hook output priority -99; policy accept;\n")
		fmt.Fprintf(&b, "    ct direction original ct mark & 0xffff0000 == 0x%08x meta mark set ct mark\n", publicMarks)
		b.WriteString("  }\n")
	}
	if len(rs.Inner) > 0 {
		// Packets for an address inside a tunnel leave through a tunnel or
		// not at all: with the tunnel down they would take the default route
		// into the datacenter's network.
		for _, ch := range [][2]string{{"leak_forward", "forward"}, {"leak_output", "output"}} {
			fmt.Fprintf(&b, "  chain %s {\n    type filter hook %s priority filter; policy accept;\n", ch[0], ch[1])
			b.WriteString("    oifname \"nt-*\" return\n    oifname \"ntx-*\" return\n    oifname \"lo\" return\n")
			for _, fam := range []string{"ip", "ip6"} {
				var ns []string
				for _, p := range rs.Inner {
					if p.Addr().Is4() == (fam == "ip") {
						ns = append(ns, p.String())
					}
				}
				if len(ns) > 0 {
					fmt.Fprintf(&b, "    %s daddr { %s } counter drop\n", fam, strings.Join(ns, ", "))
				}
			}
			b.WriteString("  }\n")
		}
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

// CarrierScript keeps connection tracking off the tunnels' own packets
// between the two servers. Once anything on the server uses NAT or a
// stateful firewall, the kernel looks up and updates a connection entry for
// every packet, these included, though they are never translated and the
// firewall lets their far ends in by address. Skipping them saves that work
// on every packet of every tunnel, in both directions.
func CarrierScript(cs []Protect) string {
	var b strings.Builder
	b.WriteString("table inet neotunnel_raw\ndelete table inet neotunnel_raw\n")
	if len(cs) == 0 {
		return b.String()
	}
	b.WriteString("table inet neotunnel_raw {\n")
	for _, ch := range [][3]string{{"from_far", "prerouting", "saddr"}, {"to_far", "output", "daddr"}} {
		fmt.Fprintf(&b, "  chain %s {\n    type filter hook %s priority raw; policy accept;\n", ch[0], ch[1])
		for _, c := range cs {
			fam := "ip"
			if c.Remote.Is6() {
				fam = "ip6"
			}
			if c.Port != 0 {
				fmt.Fprintf(&b, "    %s %s %s udp dport %d notrack\n", fam, ch[2], c.Remote, c.Port)
			} else {
				fmt.Fprintf(&b, "    %s %s %s meta l4proto %d notrack\n", fam, ch[2], c.Remote, c.Proto)
			}
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// SeenTable keeps, for a day, which client addresses opened connections to
// which forwarded ports (see SeenScript).
const SeenTable = "neotunnel_seen"

// SeenScript records each new client connection to a forwarded port in a
// set of address, protocol and port that forgets an entry a day after its
// last new connection. Its table is never deleted, only its rule replaced,
// so the day's record survives every reload of the forwards; and it only
// looks at a connection's first packet.
func SeenScript(as []Account) string {
	var b strings.Builder
	t := "inet " + SeenTable
	if len(as) == 0 {
		fmt.Fprintf(&b, "table %s\ndelete table %s\n", t, t)
		return b.String()
	}
	fmt.Fprintf(&b, "add table %s\n", t)
	for _, f := range [][2]string{{"4", "ipv4_addr"}, {"6", "ipv6_addr"}} {
		fmt.Fprintf(&b, "add set %s c%s { type %s . inet_proto . inet_service; flags dynamic,timeout; timeout 1d; size 262144; }\n", t, f[0], f[1])
	}
	fmt.Fprintf(&b, "add chain %s in { type filter hook prerouting priority -140; policy accept; }\nflush chain %s in\n", t, t)
	any := false
	for _, p := range []string{"tcp", "udp"} {
		set := portList(as, p)
		if set == "" {
			continue
		}
		if !any {
			fmt.Fprintf(&b, "add rule %s in iifname \"nt-*\" return\nadd rule %s in ct state != new return\n", t, t)
			any = true
		}
		fmt.Fprintf(&b, "add rule %s in meta nfproto ipv4 %s dport { %s } update @c4 { ip saddr . meta l4proto . %s dport }\n", t, p, set, p)
		fmt.Fprintf(&b, "add rule %s in meta nfproto ipv6 %s dport { %s } update @c6 { ip6 saddr . meta l4proto . %s dport }\n", t, p, set, p)
	}
	return b.String()
}

// counterName is the nftables named-counter prefix for a forward key; "u"
// and "d" are appended for the upload and download directions.
func counterName(key string) string {
	h := fnv.New32a()
	h.Write([]byte(key))
	return fmt.Sprintf("nt%08x", h.Sum32())
}

// portMap is the body of a map from a protocol's forwarded ports to what
// value gives each forward ("" when no forward has the protocol). A port is
// listed once.
func portMap(as []Account, proto string, value func(Account) string) string {
	seen := map[string]bool{}
	var out []string
	for _, a := range as {
		if a.Key == "" || !slices.Contains(a.Proto, proto) {
			continue
		}
		for _, p := range strings.Split(a.Ports, ",") {
			if p = strings.TrimSpace(p); p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p+" : "+value(a))
		}
	}
	return strings.Join(out, ", ")
}

// portList is the set body of a protocol's forwarded ports ("" = none).
func portList(as []Account, proto string) string {
	m := portMap(as, proto, func(Account) string { return "" })
	if m == "" {
		return ""
	}
	return strings.TrimSuffix(strings.ReplaceAll(m+", ", " : , ", ", "), ", ")
}

// counterNames returns the unique counter prefixes of the forwards, in order.
func counterNames(as []Account) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range as {
		if a.Key == "" || seen[a.Key] {
			continue
		}
		seen[a.Key] = true
		out = append(out, counterName(a.Key))
	}
	return out
}

// PlansTable holds the sold ports' limits (see PlansScript).
const PlansTable = "neotunnel_plans"

// PlansScript renders the sold ports' limits. Each port's packets reach its
// own chains through one map lookup per direction and protocol, before the
// forwards' counters, so what a limit drops isn't counted:
//
//   - from clients (plan_in, before NAT): a closed port drops everything;
//     a new address is let in while the port has fewer than MaxIPs (an
//     address leaves five minutes after its last packet); a new connection
//     while the port has fewer than MaxConns open; then the speed cap.
//   - to clients (plan_out): a closed port drops everything, then the cap.
//
// The cap drops what goes over it, as a router's policer does; the bucket
// holds a second of traffic, so TCP keeps most of the rate.
//
// The table lives on its own and is never replaced whole: its chains are
// emptied and filled again, and the sets that count each port's open
// connections and addresses stay, so a reload of the forwards (any change
// on the server) doesn't let anyone past a limit. A set's size is in its
// name; a new limit starts a new set. chains and sets are what the table
// has now; those no plan uses any more are removed.
func PlansScript(ps []Plan, chains, sets []string) string {
	t := "inet " + PlansTable
	var b strings.Builder
	if len(ps) == 0 {
		fmt.Fprintf(&b, "table %s\ndelete table %s\n", t, t)
		return b.String()
	}
	fmt.Fprintf(&b, "add table %s\n", t)
	fmt.Fprintf(&b, "add chain %s plan_in { type filter hook prerouting priority -160; policy accept; }\n", t)
	fmt.Fprintf(&b, "add chain %s plan_out { type filter hook postrouting priority 105; policy accept; }\n", t)
	for _, c := range append([]string{"plan_in", "plan_out"}, chains...) {
		fmt.Fprintf(&b, "flush chain %s %s\n", t, c)
	}
	// What the plans need now.
	needChains, needSets := map[string]bool{"plan_in": true, "plan_out": true}, map[string]bool{}
	var rules strings.Builder
	chain := func(name string, body ...string) {
		needChains[name] = true
		fmt.Fprintf(&rules, "add chain %s %s\n", t, name)
		for _, r := range body {
			fmt.Fprintf(&rules, "add rule %s %s %s\n", t, name, r)
		}
	}
	set := func(name, typ string, size int, timeout string) {
		needSets[name] = true
		fmt.Fprintf(&rules, "add set %s %s { type %s; size %d; flags dynamic%s; }\n", t, name, typ, size, timeout)
	}
	in, out := map[string]string{}, map[string]string{}
	for _, p := range ps {
		id := counterName(p.Key)[2:]
		rate := ""
		if p.RateMbit > 0 {
			rate = fmt.Sprintf("limit rate over %d bytes/second drop", int64(p.RateMbit)*125_000)
		}
		if p.Closed != "" {
			chain("pi"+id, "drop")
			chain("po"+id, "drop")
			in[p.Key], out[p.Key] = "pi"+id, "po"+id
			continue
		}
		if p.MaxConns > 0 || rate != "" {
			var body []string
			if p.MaxConns > 0 {
				c := fmt.Sprintf("c%sn%d", id, p.MaxConns)
				set(c, "ct_dir", 1, "")
				body = append(body, fmt.Sprintf("ct state new add @%s { ct direction ct count over %d } drop", c, p.MaxConns))
			}
			if rate != "" {
				body = append(body, rate)
			}
			chain("pq"+id, body...)
			in[p.Key] = "pq" + id
		}
		if p.MaxIPs > 0 {
			next := "return"
			if in[p.Key] != "" {
				next = "goto " + in[p.Key]
			}
			a4, a6 := fmt.Sprintf("a4%sn%d", id, p.MaxIPs), fmt.Sprintf("a6%sn%d", id, p.MaxIPs)
			set(a4, "ipv4_addr", p.MaxIPs, ",timeout; timeout 5m")
			set(a6, "ipv6_addr", p.MaxIPs, ",timeout; timeout 5m")
			body := []string{
				fmt.Sprintf("ip saddr @%[1]s update @%[1]s { ip saddr } %[2]s", a4, next),
				fmt.Sprintf("ip6 saddr @%[1]s update @%[1]s { ip6 saddr } %[2]s", a6, next),
				fmt.Sprintf("meta nfproto ipv4 add @%s { ip saddr } %s", a4, next),
				fmt.Sprintf("meta nfproto ipv6 add @%s { ip6 saddr } %s", a6, next),
				// Full: no new connections from another address; those
				// already open go on.
				"ct state new drop",
			}
			if next != "return" {
				body = append(body, next)
			}
			chain("pi"+id, body...)
			in[p.Key] = "pi" + id
		}
		if rate != "" {
			chain("po"+id, rate)
			out[p.Key] = "po" + id
		}
	}
	// Old chains first (nothing jumps to them any more), then old sets.
	for _, c := range chains {
		if !needChains[c] {
			fmt.Fprintf(&b, "delete chain %s %s\n", t, c)
		}
	}
	for _, s := range sets {
		if !needSets[s] {
			fmt.Fprintf(&b, "delete set %s %s\n", t, s)
		}
	}
	b.WriteString(rules.String())
	vmap := func(m map[string]string, proto string) string {
		return portMap(accountsOf(ps, m), proto, func(a Account) string { return "jump " + m[a.Key] })
	}
	fmt.Fprintf(&b, "add rule %s plan_in iifname \"nt-*\" return\nadd rule %s plan_in ct direction != original return\n", t, t)
	for _, p := range []string{"tcp", "udp"} {
		if m := vmap(in, p); m != "" {
			fmt.Fprintf(&b, "add rule %s plan_in %s dport vmap { %s }\n", t, p, m)
		}
	}
	fmt.Fprintf(&b, "add rule %s plan_out oifname \"nt-*\" return\nadd rule %s plan_out ct direction != reply return\n", t, t)
	for _, p := range []string{"tcp", "udp"} {
		if m := vmap(out, p); m != "" {
			fmt.Fprintf(&b, "add rule %s plan_out meta l4proto %s ct original proto-dst vmap { %s }\n", t, p, m)
		}
	}
	return b.String()
}

// accountsOf lists the plans' forwards that have a chain in m.
func accountsOf(ps []Plan, m map[string]string) []Account {
	var out []Account
	for _, p := range ps {
		if m[p.Key] != "" {
			out = append(out, p.Account)
		}
	}
	return out
}

// CounterName exposes the counter prefix so the daemon can read a forward's
// bytes: CounterName(proto, ports)+"u" and +"d".
func CounterName(proto, ports string) string { return counterName(proto + "/" + ports) }
