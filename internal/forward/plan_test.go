package forward

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
)

// Sold ports get their own chains in their own table, reached by one lookup
// per direction; relays are counted like the kernel's forwards; a closed
// port drops all; what a reload must keep (connection counts, addresses)
// lives in sets that stay, and old ones go.
func TestPlans(t *testing.T) {
	st := modeState()
	st.Tunnels[0].Secret = config.NewSecret()
	st.Tunnels[0].Sides[1].Endpoint = "203.0.113.9"
	fs := []config.Forward{
		{Node: "hub", Tunnel: "t1", Proto: "both", Ports: "443, 2053-2056", Plan: &config.PortPlan{RateMbit: 100, MaxConns: 50, MaxIPs: 2}},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "8443", Mode: config.ForwardRelay, Plan: &config.PortPlan{MaxIPs: 1}},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "9443", Plan: &config.PortPlan{QuotaGB: 10}},
		{Node: "hub", Tunnel: "t1", Proto: "udp", Ports: "5000", Plan: &config.PortPlan{RateMbit: 20}},
		{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "6000", Plan: &config.PortPlan{QuotaGB: 1}}, // nothing for the kernel
	}
	SetClosed(map[string]string{"tcp/9443": ClosedQuota})
	defer SetClosed(nil)
	rs, errs := Build(st, fs, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Accounts) != 5 || len(rs.Plans) != 4 {
		t.Fatalf("accounts %d plans %d", len(rs.Accounts), len(rs.Plans))
	}
	a, r, c, u := counterName("both/443, 2053-2056")[2:], counterName("tcp/8443")[2:], counterName("tcp/9443")[2:], counterName("udp/5000")[2:]
	s := PlansScript(rs.Plans, []string{"plan_in", "plan_out", "pq" + a, "pqdeadbeef"}, []string{"c" + a + "n50", "c" + a + "n40", "a4deadbeefn3"})
	if p := dumpTo(); p != "" {
		writeFile(t, p, Script(rs)+s)
	}
	pt := "inet " + PlansTable + " "
	for _, want := range []string{
		"add table inet neotunnel_plans\n",
		"flush chain " + pt + "pq" + a + "\n",
		"delete chain " + pt + "pqdeadbeef\n",
		"delete set " + pt + "c" + a + "n40\n",
		"delete set " + pt + "a4deadbeefn3\n",
		"add set " + pt + "c" + a + "n50 { type ct_dir; size 1; flags dynamic; }\n",
		"add rule " + pt + "pq" + a + " ct state new add @c" + a + "n50 { ct direction ct count over 50 } drop\n",
		"add rule " + pt + "pq" + a + " limit rate over 12500000 bytes/second drop\n",
		"add set " + pt + "a4" + a + "n2 { type ipv4_addr; size 2; flags dynamic,timeout; timeout 5m; }\n",
		"add rule " + pt + "pi" + a + " ip saddr @a4" + a + "n2 update @a4" + a + "n2 { ip saddr } goto pq" + a + "\n",
		"add rule " + pt + "po" + a + " limit rate over 12500000 bytes/second drop\n",
		// Address limit alone: nothing after it.
		"add rule " + pt + "pi" + r + " ip saddr @a4" + r + "n1 update @a4" + r + "n1 { ip saddr } return\n",
		"add rule " + pt + "pi" + c + " drop\n",
		"add rule " + pt + "po" + c + " drop\n",
		"add rule " + pt + "plan_in tcp dport vmap { 443 : jump pi" + a + ", 2053-2056 : jump pi" + a + ", 8443 : jump pi" + r + ", 9443 : jump pi" + c + " }\n",
		"add rule " + pt + "plan_in udp dport vmap { 443 : jump pi" + a + ", 2053-2056 : jump pi" + a + ", 5000 : jump pq" + u + " }\n",
		"add rule " + pt + "plan_out meta l4proto tcp ct original proto-dst vmap { 443 : jump po" + a + ", 2053-2056 : jump po" + a + ", 9443 : jump po" + c + " }\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plans missing %q:\n%s", want, s)
		}
	}
	for _, keep := range []string{"delete set " + pt + "c" + a + "n50", "delete chain " + pt + "pq" + a + "\n", "delete chain " + pt + "plan_in"} {
		if strings.Contains(s, keep) {
			t.Errorf("plans delete what they use: %q", keep)
		}
	}
	// Old chains go before the sets they used, and both before new rules.
	if strings.Index(s, "delete chain") > strings.Index(s, "delete set") || strings.Index(s, "delete set") > strings.Index(s, "add rule") {
		t.Errorf("order:\n%s", s)
	}
	if got := PlansScript(nil, nil, nil); got != "table inet neotunnel_plans\ndelete table inet neotunnel_plans\n" {
		t.Errorf("no plans: %q", got)
	}
	// The forwards' own table: relays are counted too, by the
	// connection's own port.
	m := Script(rs)
	for _, want := range []string{
		"8443 : \"nt" + r + "u\"",
		"meta l4proto tcp counter name ct original proto-dst map { 443 : \"nt" + a + "d\", 2053-2056 : \"nt" + a + "d\", 8443 : \"nt" + r + "d\"",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("script missing %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "plan_in") {
		t.Error("the plans are in the forwards' table")
	}
}

// Reloading the forwards' table keeps the counters' values.
func TestKeepCounts(t *testing.T) {
	s := "table inet neotunnel {\n  counter nt1u { }\n  counter nt1d { }\n  chain x {\n  }\n}\n"
	got := keepCounts(s, map[string]Count{"nt1u": {Bytes: 1000, Packets: 3}})
	if !strings.Contains(got, "  counter nt1u { packets 3 bytes 1000 }\n  counter nt1d { }\n") {
		t.Errorf("got:\n%s", got)
	}
}

// The day's record of clients is kept apart and never deleted while there
// are forwards; with none it goes.
func TestSeenScript(t *testing.T) {
	s := SeenScript([]Account{{Key: "both/443", Proto: []string{"tcp", "udp"}, Ports: "443, 2053-2056"}})
	for _, want := range []string{
		"add table inet neotunnel_seen\n",
		"add set inet neotunnel_seen c4 { type ipv4_addr . inet_proto . inet_service; flags dynamic,timeout; timeout 1d; size 262144; }\n",
		"flush chain inet neotunnel_seen in\n",
		"add rule inet neotunnel_seen in meta nfproto ipv4 tcp dport { 443, 2053-2056 } update @c4 { ip saddr . meta l4proto . tcp dport }\n",
		"add rule inet neotunnel_seen in meta nfproto ipv6 udp dport { 443, 2053-2056 } update @c6 { ip6 saddr . meta l4proto . udp dport }\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("seen missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "delete") {
		t.Error("the day's record is deleted")
	}
	if !strings.Contains(SeenScript(nil), "delete table inet neotunnel_seen") {
		t.Error("no forwards: the record stays")
	}
}
