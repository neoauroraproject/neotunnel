package forward

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"neotunnel/internal/config"
)

func TestParsePorts(t *testing.T) {
	got, err := ParsePorts(" 443, 2053-2096 ,8443")
	if err != nil || got != "443, 2053-2096, 8443" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "0", "70000", "9-3", "abc", "20-25", "7710"} {
		if _, err := ParsePorts(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestBuildTargetsFarEnd(t *testing.T) {
	st := &config.State{Self: "hub", Tunnels: []config.Tunnel{{
		Name: "t1", Kind: config.KindIPIP6, Net4: "10.77.0.0/30",
		Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}},
	}}}
	fs := []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443"}}
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Rules) != 1 || rs.Rules[0].Target.String() != "10.77.0.2" {
		t.Fatalf("want DNAT to 10.77.0.2, got %+v", rs.Rules)
	}
	s := Script(rs)
	for _, want := range []string{
		`tcp dport { 443 } dnat ip to 10.77.0.2`,
		`udp dport { 443 } dnat ip to 10.77.0.2`,
		`oifname "nt-t1" ct status dnat masquerade`,
		`maxseg size set rt mtu`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func TestForwardToOtherPort(t *testing.T) {
	st := &config.State{Self: "hub", Tunnels: []config.Tunnel{{
		Name: "de", Kind: config.KindIPIP6, Net4: "10.77.0.4/30",
		Sides: [2]config.Side{{Node: "hub"}, {Node: "germany"}},
	}}}
	fs := []config.Forward{{Node: "hub", Tunnel: "de", Proto: "tcp", Ports: "2053", To: 443}}
	rs, _ := Build(st, fs, []Link{{Name: "nt-de", Tunnel: "de"}})
	if s := Script(rs); !strings.Contains(s, "tcp dport { 2053 } dnat ip to 10.77.0.6:443") {
		t.Errorf("port remap missing:\n%s", s)
	}
}

func TestEmptyScriptOnlyDeletes(t *testing.T) {
	if s := Script(Ruleset{}); strings.Contains(s, "chain") {
		t.Errorf("empty ruleset should only drop the table:\n%s", s)
	}
}

func modeState() *config.State {
	return &config.State{Self: "hub", Address: "198.51.100.1", Tunnels: []config.Tunnel{{
		Name: "t1", Kind: config.KindIPIP, Net4: "10.77.0.0/30",
		Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}},
	}}}
}

func TestTransparentKeepsClientAddress(t *testing.T) {
	st := modeState()
	fs := []config.Forward{
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "443", Mode: config.ForwardTransparent},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "80"},
	}
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	s := Script(rs)
	for _, want := range []string{
		"tcp dport { 443 } ct mark set 0x4e550001 dnat ip to 10.77.0.2",
		"tcp dport { 80 } dnat ip to 10.77.0.2",
		`oifname "nt-t1" ct status dnat ct mark != 0x4e550001 masquerade`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
}

func TestReturnPathOnFarEnd(t *testing.T) {
	// edge receives hub's transparent forward with its configuration.
	hub := modeState()
	hub.Forwards = []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "443", Mode: config.ForwardTransparent}}
	ts, fs := hub.ForPeer("edge")
	if len(fs) != 1 {
		t.Fatalf("transparent forward not sent to the far end: %+v", fs)
	}
	edge := &config.State{Self: "edge", Managed: &config.Managed{Self: "edge", Tunnels: ts, Forwards: fs}}
	_, own := edge.Effective()
	rs, _ := Build(edge, own, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(rs.Rules) != 0 || len(rs.Returns) != 1 || rs.Returns[0].Mark != ReturnMark("t1") {
		t.Fatalf("far end should only get a return path: %+v", rs)
	}
	s := Script(rs)
	mark := fmt.Sprintf("0x%08x", ReturnMark("t1"))
	for _, want := range []string{
		`iifname "nt-t1" ct state new ct mark set ` + mark + "\n    ct mark " + mark + " meta mark set ct mark",
		"type route hook output",
		"ct mark " + mark + " meta mark set ct mark",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// The near end itself doesn't route replies back.
	if len(hub.ReturnPaths()) != 0 {
		t.Error("near end got a return path")
	}
}

func TestProxyModeBecomesRelays(t *testing.T) {
	st := modeState()
	fs := []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443,8000-8001", To: 9000, Mode: config.ForwardProxy}}
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Rules) != 0 || len(rs.Relays) != 6 {
		t.Fatalf("want 6 relays (3 ports x tcp+udp) and no DNAT, got %d rules %d relays", len(rs.Rules), len(rs.Relays))
	}
	r := rs.Relays[0]
	if r.Target.String() != "10.77.0.2:9000" || r.Version != 2 || r.Public.String() != "198.51.100.1" {
		t.Errorf("relay %+v", r)
	}
	fs[0].ProxyVersion = 1
	if _, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}}); len(errs) != 1 {
		t.Error("PROXY v1 over UDP accepted")
	}
}

func TestProtectScript(t *testing.T) {
	s := ProtectScript([]Protect{{Remote: netip.MustParseAddr("203.0.113.10"), Proto: 47}, {Remote: netip.MustParseAddr("2001:db8::1"), Proto: 17, Port: 4789}})
	for _, want := range []string{
		"ip saddr 203.0.113.10 meta l4proto 47 meta secpath missing drop",
		"ip6 saddr 2001:db8::1 udp dport 4789 meta secpath missing drop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

func TestLaneInterfaces(t *testing.T) {
	st := &config.State{Self: "hub"}
	st.Tunnels = []config.Tunnel{{Name: "g1", Kind: config.KindGRE, Net4: "10.77.0.0/30", Lanes: 2,
		Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}}}}
	fs := []config.Forward{{Node: "hub", Tunnel: "g1", Proto: "tcp", Ports: "80"}}
	rs, errs := Build(st, fs, []Link{{Name: "nt-g1", Tunnel: "g1"}, {Name: "nt-g1.1", Tunnel: "g1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Rules) != 1 || rs.Rules[0].Target.String() != "10.77.0.2" {
		t.Fatalf("rules %+v", rs.Rules)
	}
	if s := Script(rs); !strings.Contains(s, `oifname { "nt-g1", "nt-g1.1" } ct status dnat masquerade`) {
		t.Errorf("masquerade must cover every lane:\n%s", s)
	}
}
