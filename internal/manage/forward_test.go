package manage

import (
	"testing"

	"neotunnel/internal/config"
)

// Editing a forward can move it to other ports and another tunnel; the
// ports must still be free.
func TestUpdateForwardMovesPortsAndTunnel(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	if _, err := config.Update(func(s *config.State) error {
		s.Self, s.Address, s.AgentPort = "ir", "192.0.2.1", 7710
		far := config.Side{Endpoint: "198.51.100.1"}
		s.Tunnels = []config.Tunnel{
			{Name: "t1", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "ir", Endpoint: "192.0.2.1"}, far}},
			{Name: "t2", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "ir", Endpoint: "192.0.2.1"}, far}},
		}
		s.Forwards = []config.Forward{{Node: "ir", Tunnel: "t1", Ports: "443", Proto: "both"}, {Node: "ir", Tunnel: "t1", Ports: "8443", Proto: "both"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForward(ForwardRequest{WasTunnel: "t1", WasPorts: "443", Tunnel: "t2", Ports: "8443", Proto: "tcp", Mode: "nat"}); err == nil {
		t.Error("8443 is already forwarded")
	}
	if _, err := UpdateForward(ForwardRequest{WasTunnel: "t1", WasPorts: "443", Tunnel: "t2", Ports: " 2053-2054 ", Proto: "tcp", To: 443, Mode: "nat"}); err != nil {
		t.Fatal(err)
	}
	st, _ := config.Load()
	if f := st.Forwards[0]; f.Tunnel != "t2" || f.Ports != "2053-2054" || f.Proto != "tcp" || f.To != 443 || len(st.Forwards) != 2 {
		t.Errorf("forwards %+v", st.Forwards)
	}
}
