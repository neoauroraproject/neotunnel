package manage

import (
	"testing"

	"neotunnel/internal/config"
)

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
