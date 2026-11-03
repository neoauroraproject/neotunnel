package manage

import (
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/traffic"
)

// A plan's period starts when it is set and stays through edits; a renewal
// starts a new one and moves the end on; editing the port keeps both the
// plan and its traffic.
func TestPortPlan(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	if _, err := config.Update(func(s *config.State) error {
		s.Self, s.Address, s.AgentPort = "ir", "192.0.2.1", 7710
		s.Tunnels = []config.Tunnel{{Name: "t1", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "ir", Endpoint: "192.0.2.1"}, {Endpoint: "198.51.100.1"}}}}
		s.Forwards = []config.Forward{{Node: "ir", Tunnel: "t1", Ports: "443", Proto: "tcp"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan := func() config.PortPlan {
		st, _ := config.Load()
		if st.Forwards[0].Plan == nil {
			t.Fatal("no plan")
		}
		return *st.Forwards[0].Plan
	}
	if _, err := SetPortPlan("t1", "443", &config.PortPlan{RateMbit: -1}, false); err == nil {
		t.Error("a negative cap was taken")
	}
	if _, err := SetPortPlan("t1", "443", &config.PortPlan{Customer: " Ali ", QuotaGB: 50, MaxIPs: 2}, false); err != nil {
		t.Fatal(err)
	}
	p := plan()
	if p.Customer != "Ali" || p.Start == 0 || p.QuotaGB != 50 {
		t.Fatalf("plan %+v", p)
	}
	// Backdate the start: an edit keeps it, a restart doesn't.
	config.Update(func(s *config.State) error { s.Forwards[0].Plan.Start = 1000; return nil })
	SetPortPlan("t1", "443", &config.PortPlan{QuotaGB: 60}, false)
	if p := plan(); p.Start != 1000 || p.QuotaGB != 60 {
		t.Errorf("edit: %+v", p)
	}
	end := time.Now().Unix() + 5*86400
	config.Update(func(s *config.State) error {
		s.Forwards[0].Plan.Expires, s.Forwards[0].Plan.Paused = end, true
		return nil
	})
	if _, err := RenewPort("t1", "443", 30, false); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p.Start != 1000 || p.Expires != end+30*86400 || p.Paused {
		t.Errorf("renew keeps the traffic used: %+v", p)
	}
	if _, err := RenewPort("t1", "443", 0, true); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p.Start < time.Now().Unix()-5 || p.Expires != end+30*86400 {
		t.Errorf("renew with reset: %+v", p)
	}
	if _, err := UpdateForward(ForwardRequest{WasTunnel: "t1", WasPorts: "443", Tunnel: "t1", Ports: "8443", Proto: "tcp", Mode: "nat"}); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p.QuotaGB != 60 {
		t.Errorf("the edit lost the plan: %+v", p)
	}
	if r := traffic.TakeRenames(); len(r) != 1 || r[0] != [2]string{traffic.Port + "tcp/443", traffic.Port + "tcp/8443"} {
		t.Errorf("renames %v", r)
	}
	SetPortPlan("t1", "8443", nil, false)
	if st, _ := config.Load(); st.Forwards[0].Plan != nil {
		t.Error("plan kept")
	}
	if _, err := RenewPort("t1", "8443", 30, false); err == nil {
		t.Error("renewed a port that isn't sold")
	}
}
