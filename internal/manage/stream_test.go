package manage

import (
	"testing"

	"neotunnel/internal/config"
)

func TestPlanConnectionTunnel(t *testing.T) {
	st := testState()
	st.Peers[0].StreamTunnels = true
	for _, mode := range []string{"tcpmux", "tlsmux"} {
		_, defs, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: mode, Settings: Settings{Reverse: true, SNI: "example.com", Conns: 6}})
		if err != nil {
			t.Fatal(mode, err)
		}
		d := defs[0]
		if len(defs) != 1 || !d.IsStream() || !d.Reverse || d.Conns != 6 || d.Port < 20000 {
			t.Fatalf("%s: %+v", mode, d)
		}
		if (d.SNI != "") != (mode == "tlsmux") {
			t.Errorf("%s: SNI %q", mode, d.SNI)
		}
		if err := st.Allocate(&d); err != nil {
			t.Fatal(err)
		}
		if d.Net4 != "" || d.Net6 != "" || d.Secret == "" || len(d.StreamPorts()) != config.MaxRelayPorts {
			t.Errorf("%s after allocation: %+v", mode, d)
		}
		if d.Listener() != 0 {
			t.Errorf("%s: reverse, yet side %d listens", mode, d.Listener())
		}
	}
	if _, _, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: "tcpmux", Obfs: true}); err == nil {
		t.Error("a connection tunnel over NeoObfs was planned")
	}
	if _, _, err := PlanTunnel(st, TunnelRequest{Remote: "203.0.113.9", Mode: "tcpmux"}); err == nil {
		t.Error("a connection tunnel to a device without NeoTunnel was planned")
	}
	st.Peers[0].StreamTunnels, st.Peers[0].PublicKey = false, "key"
	if _, _, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: "tlsmux"}); err == nil {
		t.Error("a connection tunnel to an older server was planned")
	}
}

func TestRelaySecretGivesPorts(t *testing.T) {
	st := testState()
	st.Tunnels = []config.Tunnel{{Name: "g", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}}}}
	relaySecret(st, &st.Tunnels[0], config.ForwardRelay)
	if g := st.Tunnels[0]; g.Secret == "" || len(g.RelayPorts) != 0 {
		t.Fatalf("an older far server: %+v", g)
	}
	st.Peers[0].StreamTunnels = true
	relaySecret(st, &st.Tunnels[0], config.ForwardRelay)
	g := st.Tunnels[0]
	if len(g.RelayPorts) != config.MaxRelayPorts {
		t.Fatalf("relay ports %v", g.RelayPorts)
	}
	for _, p := range g.RelayPorts {
		if p < 20000 || p >= 60000 || p == config.LegacyRelayPort {
			t.Errorf("relay port %d", p)
		}
	}
}
