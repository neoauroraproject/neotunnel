// Package guard is the firewall for the main server's public interface.
//
// Everything that reaches the server from outside is dropped unless this
// server uses it:
//
//   - always, and not configurable: SSH (the ports sshd listens on, those of
//     open SSH sessions, and 22), the panel's port, everything from this
//     server's peers and tunnel endpoints (tunnels, IPsec, the agent),
//     replies to connections already open, and the ICMP that path MTU
//     discovery and IPv6 neighbour discovery need;
//   - the ports programs listen on (a new 3x-ui inbound on a random port is
//     opened on its own, a port nobody listens on is closed), and this
//     server's forwards: each open to everyone, to Iranian addresses only
//     (geoip:ir), or closed;
//   - ping, as chosen: closed, Iran only, or open;
//   - address lists: allowed always, blocked always (never SSH or the panel).
//
// Only the public interfaces are filtered: tunnels, the loopback and private
// networks are left alone. The rules live in their own nftables table,
// replaced in one transaction whenever what the server listens on changes.
package guard

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"neotunnel/internal/config"
)

// Table is the guard's nftables table.
const Table = "neotunnel_guard"

// Socket is a port a program listens on.
type Socket struct {
	Proto   string // tcp or udp
	Port    uint16
	Process string // "" when the kernel owns it (tunnel ports)
}

func (s Socket) Key() string { return s.Proto + "/" + strconv.Itoa(int(s.Port)) }

// Port is one port the guard opens, and to whom.
type Port struct {
	Proto   string `json:"proto"`
	Port    uint16 `json:"port"`
	Scope   string `json:"scope"`             // all, iran or closed
	Process string `json:"process,omitempty"` // what listens on it
	Forward bool   `json:"forward,omitempty"` // a forward of this server
	// Set is true when Scope comes from the settings, not the default.
	Set bool `json:"set,omitempty"`
}

func (p Port) Key() string { return p.Proto + "/" + strconv.Itoa(int(p.Port)) }

// Input is everything a ruleset is made from.
type Input struct {
	Ifaces []string     // interfaces to guard
	Peers  []netip.Addr // let in whole
	SSH    []uint16     // never closed
	Panel  uint16       // never closed
	Ports  []Port
	ICMP   string
	Allow  []string
	Block  []string
	IR     []netip.Prefix
}

// IsXray reports whether a process is an Xray core (3x-ui's inbounds).
func IsXray(process string) bool {
	p := strings.ToLower(process)
	return strings.HasPrefix(p, "xray") || strings.HasPrefix(p, "v2ray") || strings.HasPrefix(p, "sing-box")
}

// skipProcess: sockets of these programs are covered otherwise (NeoTunnel's
// own: the agent and test ports are for peers, forwards come from the
// configuration; sshd: always open).
func skipProcess(p string) bool {
	switch p {
	case "neotunnel", "sshd":
		return true
	}
	return false
}

// Classify lists the ports to open: the forwards and what programs listen
// on, each with its scope. Forwards and Xray inbounds follow ConfigIran; a
// port set in g.Ports keeps that; everything else is open to everyone.
// Kernel-owned sockets (tunnel ports) and ports that are always open are
// left out.
func Classify(g config.Guard, forwards, sockets []Socket, always map[string]bool) []Port {
	byKey := map[string]*Port{}
	var order []string
	add := func(s Socket, forward bool) {
		k := s.Key()
		if always[k] {
			return
		}
		if p, ok := byKey[k]; ok {
			p.Forward = p.Forward || forward
			if p.Process == "" {
				p.Process = s.Process
			}
			return
		}
		byKey[k] = &Port{Proto: s.Proto, Port: s.Port, Process: s.Process, Forward: forward}
		order = append(order, k)
	}
	for _, f := range forwards {
		add(f, true)
	}
	for _, s := range sockets {
		if s.Process == "" || skipProcess(s.Process) {
			continue
		}
		add(s, false)
	}
	out := make([]Port, 0, len(order))
	for _, k := range order {
		p := *byKey[k]
		p.Scope = config.ScopeAll
		if g.ConfigIran && (p.Forward || IsXray(p.Process)) {
			p.Scope = config.ScopeIran
		}
		if s, ok := g.Ports[k]; ok && validScope(s) {
			p.Scope, p.Set = s, true
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

func validScope(s string) bool {
	return s == config.ScopeAll || s == config.ScopeIran || s == config.ScopeClosed
}

// ValidICMP checks a ping setting.
func ValidICMP(s string) bool { return s == "" || s == "closed" || s == "iran" || s == "open" }

// ParseEntry reads an address list entry: an IP, a CIDR range or geoip:ir.
func ParseEntry(s string) (prefix netip.Prefix, geo bool, err error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "geoip:ir") {
		return netip.Prefix{}, true, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "geoip:") {
		return netip.Prefix{}, false, fmt.Errorf("%s: only geoip:ir is available", s)
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), false, nil
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), false, nil
	}
	return netip.Prefix{}, false, fmt.Errorf("%q is not an IP, a range (CIDR) or geoip:ir", s)
}

// expand turns an address list into prefixes, geoip:ir included.
func expand(list []string, ir []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, e := range list {
		p, geo, err := ParseEntry(e)
		switch {
		case err != nil:
			continue
		case geo:
			out = append(out, ir...)
		default:
			out = append(out, p)
		}
	}
	return out
}

func split(ps []netip.Prefix) (v4, v6 []string) {
	for _, p := range ps {
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	return v4, v6
}

func addrs(as []netip.Addr) (v4, v6 []string) {
	seen := map[netip.Addr]bool{}
	for _, a := range as {
		a = a.Unmap()
		if !a.IsValid() || seen[a] {
			continue
		}
		seen[a] = true
		if a.Is4() {
			v4 = append(v4, a.String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	return v4, v6
}

// Script renders the guard's nftables transaction; with no interfaces it
// only removes the table.
func Script(in Input) string {
	var b strings.Builder
	// Creating then deleting makes the delete safe when the table doesn't exist.
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", Table, Table)
	if len(in.Ifaces) == 0 {
		return b.String()
	}
	set := func(name, typ string, interval bool, elems []string) {
		fmt.Fprintf(&b, "  set %s {\n    type %s;\n", name, typ)
		if interval {
			b.WriteString("    flags interval;\n    auto-merge;\n")
		}
		if len(elems) > 0 {
			fmt.Fprintf(&b, "    elements = { %s }\n", strings.Join(elems, ", "))
		}
		b.WriteString("  }\n")
	}
	ir4, ir6 := split(in.IR)
	al4, al6 := split(expand(in.Allow, in.IR))
	bl4, bl6 := split(expand(in.Block, in.IR))
	pe4, pe6 := addrs(in.Peers)
	var all, iran []string
	for _, p := range in.Ports {
		e := fmt.Sprintf("%s . %d", p.Proto, p.Port)
		switch p.Scope {
		case config.ScopeAll:
			all = append(all, e)
		case config.ScopeIran:
			iran = append(iran, e)
		}
	}
	var ssh []string
	for _, p := range in.SSH {
		ssh = append(ssh, strconv.Itoa(int(p)))
	}
	if in.Panel != 0 {
		ssh = append(ssh, strconv.Itoa(int(in.Panel)))
	}
	ifaces := make([]string, len(in.Ifaces))
	for i, n := range in.Ifaces {
		ifaces[i] = strconv.Quote(n)
	}

	fmt.Fprintf(&b, "table inet %s {\n", Table)
	b.WriteString("  counter dropped {\n    packets 0 bytes 0\n  }\n")
	set("ir4", "ipv4_addr", true, ir4)
	set("ir6", "ipv6_addr", true, ir6)
	set("allow4", "ipv4_addr", true, al4)
	set("allow6", "ipv6_addr", true, al6)
	set("block4", "ipv4_addr", true, bl4)
	set("block6", "ipv6_addr", true, bl6)
	set("peers4", "ipv4_addr", false, pe4)
	set("peers6", "ipv6_addr", false, pe6)
	set("open_all", "inet_proto . inet_service", false, all)
	set("open_iran", "inet_proto . inet_service", false, iran)

	// What both chains start with: only the guarded interfaces, replies and
	// peers, then the address lists.
	head := func(hook string) {
		fmt.Fprintf(&b, "  chain %s {\n    type filter hook %s priority filter - 10; policy accept;\n", hook, hook)
		fmt.Fprintf(&b, "    iifname != { %s } accept\n", strings.Join(ifaces, ", "))
		b.WriteString("    ct state established,related accept\n")
		b.WriteString("    ip saddr @peers4 accept\n    ip6 saddr @peers6 accept\n")
	}
	tail := func(dport string) {
		b.WriteString("    ip saddr @block4 drop\n    ip6 saddr @block6 drop\n")
		b.WriteString("    ip saddr @allow4 accept\n    ip6 saddr @allow6 accept\n")
		fmt.Fprintf(&b, "    meta l4proto . %s @open_all accept\n", dport)
		fmt.Fprintf(&b, "    meta l4proto . %s @open_iran ip saddr @ir4 accept\n", dport)
		fmt.Fprintf(&b, "    meta l4proto . %s @open_iran ip6 saddr @ir6 accept\n", dport)
		b.WriteString("    counter name dropped drop\n  }\n")
	}

	head("input")
	if len(ssh) > 0 {
		// SSH and the panel come before the block list: a mistake in a list
		// must never lock the admin out.
		fmt.Fprintf(&b, "    tcp dport { %s } accept\n", strings.Join(ssh, ", "))
	}
	b.WriteString("    ct state invalid drop\n")
	b.WriteString("    icmp type { destination-unreachable, time-exceeded, parameter-problem } accept\n")
	b.WriteString("    icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-neighbor-solicit, nd-neighbor-advert, nd-router-advert, nd-router-solicit, nd-redirect } accept\n")
	switch in.ICMP {
	case "open":
		b.WriteString("    icmp type echo-request accept\n    icmpv6 type echo-request accept\n")
	case "iran":
		b.WriteString("    icmp type echo-request ip saddr @ir4 accept\n    icmpv6 type echo-request ip6 saddr @ir6 accept\n")
	}
	tail("th dport")

	// Forwards arrive here after their DNAT: matched by the port they were
	// sent to before it.
	head("forward")
	tail("ct original proto-dst")
	b.WriteString("}\n")
	return b.String()
}
