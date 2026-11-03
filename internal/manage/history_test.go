package manage

import (
	"testing"

	"neotunnel/internal/config"
)

// Every change to the network is kept with what it did; a rollback puts
// the network back, keeps the rest (the panel, the peers), and can itself
// be rolled back.
func TestHistoryAndRollback(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	if _, err := config.Update(func(s *config.State) error {
		s.Self, s.Address, s.AgentPort = "ir", "192.0.2.1", 7710
		s.Tunnels = []config.Tunnel{{Name: "t1", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "ir", Endpoint: "192.0.2.1"}, {Endpoint: "198.51.100.1"}}}}
		s.Forwards = []config.Forward{{Node: "ir", Tunnel: "t1", Ports: "443", Proto: "tcp"}}
		s.Panel = &config.Panel{User: "admin"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddForward(ForwardRequest{Tunnel: "t1", Ports: "8443", Proto: "tcp", Mode: "nat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveForward("t1", "443"); err != nil {
		t.Fatal(err)
	}
	// Not the network: no entry.
	if err := SetPortOrder([]string{"tcp/8443"}); err != nil {
		t.Fatal(err)
	}
	h := config.History()
	if len(h) != 2 {
		t.Fatalf("history %+v", h)
	}
	if c := h[0].Changes; len(c) != 1 || c[0] != (config.Diff{Kind: "port", Op: "removed", Name: "tcp/443"}) {
		t.Errorf("newest %+v", c)
	}
	if c := h[1].Changes; len(c) != 1 || c[0] != (config.Diff{Kind: "port", Op: "added", Name: "tcp/8443"}) {
		t.Errorf("oldest %+v", c)
	}
	// Meanwhile the panel's user changes; a rollback leaves it.
	config.Update(func(s *config.State) error { s.Panel.User = "boss"; return nil })
	if _, err := Rollback(h[1].ID); err != nil {
		t.Fatal(err)
	}
	st, _ := config.Load()
	if len(st.Forwards) != 1 || st.Forwards[0].Ports != "443" || st.Panel.User != "boss" {
		t.Errorf("after rollback: forwards %+v user %s", st.Forwards, st.Panel.User)
	}
	h = config.History()
	if len(h) != 3 || h[0].Rollback == "" || len(h[0].Changes) != 2 {
		t.Fatalf("rollback entry %+v", h[0])
	}
	// Undo the rollback.
	if _, err := Rollback(h[0].ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := config.Load(); len(st.Forwards) != 1 || st.Forwards[0].Ports != "8443" {
		t.Errorf("undoing the rollback: %+v", st.Forwards)
	}
	if _, err := Rollback("nope"); err == nil {
		t.Error("rolled back to nothing")
	}
}
