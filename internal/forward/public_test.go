package forward

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
)

// publicState is t1 (IPIP) from hub to edge's public 203.0.113.2, and v1, a
// VXLAN to the same server.
func publicState() *config.State {
	sides := [2]config.Side{{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "203.0.113.2"}}
	return &config.State{Self: "hub", Address: "198.51.100.1", Tunnels: []config.Tunnel{
		{Name: "t1", Kind: config.KindIPIP, Net4: "10.77.0.0/30", Sides: sides},
		{Name: "v1", Kind: config.KindVXLAN, Net4: "10.77.0.4/30", Net6: "fd00:1::/64", Sides: sides},
	}}
}

// A forward to the far server's public address: DNAT to that address, its
// connections marked, and the mark routed through the tunnel.
func TestPublicTarget(t *testing.T) {
	st := publicState()
	links := []Link{{Name: "nt-t1", Tunnel: "t1"}, {Name: "nt-v1", Tunnel: "v1"}}
	fs := []config.Forward{
		{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443", Public: true},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "8443", To: 443, Mode: config.ForwardProxy, Public: true},
	}
	rs, errs := Build(st, fs, links)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	mark := PublicMark("t1")
	if len(rs.Rules) != 1 || rs.Rules[0].Target.String() != "203.0.113.2" || rs.Rules[0].Mark != mark {
		t.Fatalf("rules %+v", rs.Rules)
	}
	if len(rs.Relays) != 1 || rs.Relays[0].Target.String() != "203.0.113.2:443" || rs.Relays[0].Mark != mark {
		t.Fatalf("relays %+v", rs.Relays)
	}
	if len(rs.Publics) != 1 || rs.Publics[0].Dest.String() != "203.0.113.2" || rs.Publics[0].Via.String() != "10.77.0.2" || rs.Publics[0].Iface != "nt-t1" {
		t.Fatalf("publics %+v", rs.Publics)
	}
	s := Script(rs)
	for _, want := range []string{
		"tcp dport { 443 } ct mark set 0x" + hex8(mark) + " dnat ip to 203.0.113.2",
		"chain public_in {\n    type filter hook prerouting priority -99;",
		"ct direction original ct mark & 0xffff0000 == 0x4e560000 meta mark set ct mark",
		"chain public_out {\n    type route hook output priority -99;",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// The mark would go onto VXLAN's own packets too; transparent keeps
	// the client's address, which the public address can't answer.
	for _, f := range []config.Forward{
		{Node: "hub", Tunnel: "v1", Proto: "tcp", Ports: "443", Public: true},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "443", Mode: config.ForwardTransparent, Public: true},
	} {
		if _, errs := Build(st, []config.Forward{f}, links); len(errs) != 1 {
			t.Errorf("%s %s to the public address accepted", f.Tunnel, f.ModeOf())
		}
	}
}

// Addresses inside the tunnels never leave by another interface, even with
// every tunnel down.
func TestLeakChains(t *testing.T) {
	st := publicState()
	st.Tunnels = append(st.Tunnels, config.Tunnel{Name: "t2", Kind: config.KindIPIP, Net4: "10.77.0.0/30", Sides: st.Tunnels[0].Sides})
	rs, errs := Build(st, nil, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Inner) != 3 {
		t.Fatalf("inner %v: want each subnet once", rs.Inner)
	}
	s := Script(rs)
	for _, want := range []string{
		"chain leak_forward {\n    type filter hook forward priority filter;",
		"chain leak_output {\n    type filter hook output priority filter;",
		`oifname "nt-*" return`,
		`oifname "lo" return`,
		"ip daddr { 10.77.0.0/30, 10.77.0.4/30 } counter drop",
		"ip6 daddr { fd00:1::/64 } counter drop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func hex8(m uint32) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[m&0xf]
		m >>= 4
	}
	return string(b)
}

// With NT_DUMP set, the scripts are written there for a check with a real
// nft (nft -c -f).
func TestDumpPublicScripts(t *testing.T) {
	dir := dumpTo()
	if dir == "" {
		t.Skip("NT_DUMP not set")
	}
	st := publicState()
	fs := []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443", Public: true}}
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}, {Name: "nt-v1", Tunnel: "v1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	writeFile(t, dir+"/public.nft", Script(rs))
}
