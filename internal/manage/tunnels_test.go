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

func TestNewTunnelChecksEoIPTunnelID(t *testing.T) {
	id := func(v uint16) *uint16 { return &v }
	st := &config.State{Self: "a", Address: "2001:db8::1"}
	cases := []struct {
		kind, remote string
		port         uint16
		id           *uint16
		ok           bool
	}{
		{"eoip", "203.0.113.10", 0, id(65535), true},
		{"eoip", "2001:db8::2", 0, id(4095), true},
		{"eoip", "2001:db8::2", 0, id(4096), false},
		{"eoip", "2001:db8::2", 4000, id(4096), true},
		{"gre", "203.0.113.10", 0, id(1), false},
	}
	for _, c := range cases {
		o := TunnelOpts{Kind: c.kind, Remote: c.remote, Port: c.port}
		o.TunnelID = c.id
		tn, err := NewTunnel(st, "e1", o)
		if (err == nil) != c.ok {
			t.Errorf("%s to %s port %d id %d: err %v", c.kind, c.remote, c.port, *c.id, err)
		}
		if err == nil && (tn.TunnelID == nil || *tn.TunnelID != *c.id) {
			t.Errorf("%s to %s: tunnel id %v not kept", c.kind, c.remote, tn.TunnelID)
		}
	}
}
