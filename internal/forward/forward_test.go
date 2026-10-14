package forward

import (
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
	st := &config.State{Self: "iran", Tunnels: []config.Tunnel{{
		Name: "t1", Kind: config.KindIPIP6, Net4: "10.77.0.0/30",
		Sides: [2]config.Side{{Node: "iran"}, {Node: "kharej"}},
	}}}
	fs := []config.Forward{{Node: "iran", Tunnel: "t1", Proto: "both", Ports: "443"}}
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
	st := &config.State{Self: "iran", Tunnels: []config.Tunnel{{
		Name: "de", Kind: config.KindIPIP6, Net4: "10.77.0.4/30",
		Sides: [2]config.Side{{Node: "iran"}, {Node: "germany"}},
	}}}
	fs := []config.Forward{{Node: "iran", Tunnel: "de", Proto: "tcp", Ports: "2053", To: 443}}
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
