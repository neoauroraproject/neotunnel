package manage

import (
	"testing"

	"neotunnel/internal/config"
)

func testState() *config.State {
	return &config.State{Self: "hub", Address: "198.51.100.1", Peers: []config.Peer{{Name: "edge", Address: "203.0.113.2"}}}
}

func TestPlanTCPTunnel(t *testing.T) {
	st := testState()
	name, defs, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: "tcp", Settings: Settings{Encrypt: "aes-gcm-256"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("want one tunnel, got %d", len(defs))
	}
	d := defs[0]
	if d.Name != name || d.Kind != config.KindTCP || d.Port < 31443 || d.Encrypt != "aes-gcm-256" || !d.NeedsSecret() {
		t.Errorf("tcp tunnel: %+v", d)
	}

	if _, _, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: "tcp", Obfs: true}); err == nil {
		t.Error("TCP over NeoObfs was planned")
	}
	if _, _, err := PlanTunnel(st, TunnelRequest{Remote: "203.0.113.9", Mode: "tcp"}); err == nil {
		t.Error("TCP to a device without NeoTunnel was planned")
	}
	if _, _, err := PlanTunnel(st, TunnelRequest{Peer: "edge", Mode: "tcp", Port: 7710}); err == nil {
		t.Error("TCP on the agent's port was planned")
	}
}

func TestFreePortSkipsUsedPorts(t *testing.T) {
	st := testState()
	st.Tunnels = []config.Tunnel{{Name: "o", Kind: config.KindObfs, Port: 31443, Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}}}}
	if p := FreePort(st, config.KindTCP); p != 31444 {
		t.Errorf("got port %d", p)
	}
}
