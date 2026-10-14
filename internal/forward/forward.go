// Package forward renders port forwarding and MSS clamping as one nftables
// table ("inet neotunnel") that is replaced atomically on every apply.
package forward

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"neotunnel/internal/config"
)

// Reserved ports are never forwarded so a typo can't lock you out.
var Reserved = map[int]string{22: "SSH", 7710: "neotunnel agent", 7711: "neotunnel probe"}

type Rule struct {
	Iface  string
	Proto  []string // tcp and/or udp
	Ports  string   // nft set body: "443, 8080-8090"
	Target netip.Addr
	To     uint16 // target port; 0 = keep the original port
}

type Ruleset struct {
	Rules  []Rule
	Ifaces []string // tunnel interfaces that get MSS clamping
}

// ParsePorts validates "443,8080-8090" and returns the nft set body.
func ParsePorts(s string) (string, error) {
	var parts []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(f, "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return "", fmt.Errorf("bad port %q", f)
		}
		for p, name := range Reserved {
			if p >= a && p <= b {
				return "", fmt.Errorf("port %d is reserved for %s", p, name)
			}
		}
		if isRange {
			parts = append(parts, fmt.Sprintf("%d-%d", a, b))
		} else {
			parts = append(parts, strconv.Itoa(a))
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no ports given")
	}
	return strings.Join(parts, ", "), nil
}

// ValidateTo checks a forward's target port.
func ValidateTo(to int) error {
	if to < 0 || to > 65535 {
		return fmt.Errorf("bad target port %d", to)
	}
	return nil
}

func protos(p string) ([]string, error) {
	switch p {
	case "", "tcp":
		return []string{"tcp"}, nil
	case "udp":
		return []string{"udp"}, nil
	case "both":
		return []string{"tcp", "udp"}, nil
	}
	return nil, fmt.Errorf("bad protocol %q (tcp, udp, both)", p)
}

// Link is a running tunnel interface.
type Link struct {
	Name   string
	Tunnel string
}

// Build resolves forwards to concrete DNAT targets: the far end's inner address.
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
		rs.Rules = append(rs.Rules, Rule{Iface: iface, Proto: ps, Ports: ports, Target: target.Addr(), To: f.To})
	}
	return rs, errs
}

// Script renders the full nft transaction. An empty ruleset just removes the table.
func Script(rs Ruleset) string {
	var b strings.Builder
	// Creating then deleting makes the delete safe when the table doesn't exist.
	b.WriteString("table inet neotunnel\ndelete table inet neotunnel\n")
	if len(rs.Rules) == 0 && len(rs.Ifaces) == 0 {
		return b.String()
	}
	b.WriteString("table inet neotunnel {\n")
	b.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
	for _, r := range rs.Rules {
		fam, nat := "ipv4", "ip"
		target := r.Target.String()
		if r.Target.Is6() {
			fam, nat, target = "ipv6", "ip6", "["+target+"]"
		}
		if r.To != 0 {
			target += ":" + strconv.Itoa(int(r.To))
		}
		for _, p := range r.Proto {
			fmt.Fprintf(&b, "    meta nfproto %s iifname != \"nt-*\" fib daddr type local %s dport { %s } dnat %s to %s\n",
				fam, p, r.Ports, nat, target)
		}
	}
	b.WriteString("  }\n")
	b.WriteString("  chain postrouting {\n    type nat hook postrouting priority srcnat; policy accept;\n")
	seen := map[string]bool{}
	for _, r := range rs.Rules {
		if !seen[r.Iface] {
			seen[r.Iface] = true
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
	b.WriteString("  }\n}\n")
	return b.String()
}
