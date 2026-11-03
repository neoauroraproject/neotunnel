package forward

import (
	"net/netip"
	"strings"
	"testing"

	"neotunnel/internal/config"
)

// Port counters are one map lookup per protocol, not a rule per forward;
// a port of both families is listed once, ranges included.
func TestAccountingMaps(t *testing.T) {
	st := modeState()
	fs := []config.Forward{
		{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443, 2053-2056"},
		{Node: "hub", Tunnel: "t1", Proto: "udp", Ports: "8443", To: 443},
	}
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	s := Script(rs)
	if p := dumpTo(); p != "" {
		writeFile(t, p, s)
	}
	_, in, _ := strings.Cut(s, "chain account_in {")
	in, _, _ = strings.Cut(in, "  }\n")
	if strings.Count(in, "counter name") != 2 || !strings.Contains(in, "iifname \"nt-*\" return") {
		t.Errorf("account_in:\n%s", in)
	}
	for _, want := range []string{"tcp dport map { 443 : \"nt", "2053-2056 : \"nt", "udp dport map { 443 : \"nt", "8443 : \"nt"} {
		if !strings.Contains(in, want) {
			t.Errorf("account_in missing %q:\n%s", want, in)
		}
	}
	if strings.Count(in, "443 : ") != 3 {
		t.Errorf("a port listed twice:\n%s", in)
	}
	_, out, _ := strings.Cut(s, "chain account_local_out {")
	out, _, _ = strings.Cut(out, "  }\n")
	if strings.Count(out, "fib daddr") != 1 {
		t.Errorf("account_local_out looks up routes more than once:\n%s", out)
	}
}

func TestCarrierScript(t *testing.T) {
	s := CarrierScript([]Protect{
		{Remote: netip.MustParseAddr("203.0.113.9"), Proto: 41},
		{Remote: netip.MustParseAddr("2001:db8::9"), Proto: 17, Port: 5555},
	})
	for _, want := range []string{
		"type filter hook prerouting priority raw;",
		"ip saddr 203.0.113.9 meta l4proto 41 notrack\n",
		"ip6 saddr 2001:db8::9 udp dport 5555 notrack\n",
		"type filter hook output priority raw;",
		"ip daddr 203.0.113.9 meta l4proto 41 notrack\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if p := dumpTo(); p != "" {
		writeFile(t, p+".raw", s)
	}
	if CarrierScript(nil) != "table inet neotunnel_raw\ndelete table inet neotunnel_raw\n" {
		t.Error("nothing to skip still leaves a table")
	}
}
