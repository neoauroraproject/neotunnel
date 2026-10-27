package forward

import (
	"net/netip"
	"testing"
)

func TestChangedFlows(t *testing.T) {
	a := netip.MustParseAddr("10.77.0.2")
	b := netip.MustParseAddr("10.77.0.6")
	before := flowTargets(Ruleset{Rules: []Rule{
		{Iface: "nt-t1", Proto: []string{"tcp", "udp"}, Ports: "443", Target: a},
		{Iface: "nt-t1", Proto: []string{"tcp"}, Ports: "8080-8081", Target: a},
	}})
	// 443 moved to t2, 8081 removed, 8080 unchanged.
	now := flowTargets(Ruleset{Rules: []Rule{
		{Iface: "nt-t2", Proto: []string{"tcp", "udp"}, Ports: "443", Target: b},
		{Iface: "nt-t1", Proto: []string{"tcp"}, Ports: "8080", Target: a},
	}})
	got := changedFlows(before, now)
	for _, k := range []string{"tcp/443", "udp/443", "tcp/8081"} {
		if !got[k] {
			t.Errorf("%s should start over", k)
		}
	}
	if got["tcp/8080"] || len(got) != 3 {
		t.Errorf("changed: %v", got)
	}
}
