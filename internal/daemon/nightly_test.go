package daemon

import (
	"testing"
	"time"

	"neotunnel/internal/config"
)

func TestNextNight(t *testing.T) {
	// 01:00 UTC is 04:30 Tehran: the next one is a day later.
	at := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	if got := nextNight(at); !got.Equal(at.Add(24 * time.Hour)) {
		t.Errorf("at 04:30 Tehran: %v", got)
	}
	if got := nextNight(at.Add(-time.Minute)); !got.Equal(at) {
		t.Errorf("a minute before: %v", got)
	}
	if got := nextNight(at.Add(time.Hour)); !got.Equal(at.Add(24 * time.Hour)) {
		t.Errorf("an hour after: %v", got)
	}
}

// Only what a user sees, on, paired and without a quota is tested.
func TestNightlyTunnels(t *testing.T) {
	side := func(n string) [2]config.Side { return [2]config.Side{{Node: "ir"}, {Node: n}} }
	st := &config.State{Self: "ir", Peers: []config.Peer{{Name: "de", PublicKey: "k"}, {Name: "fi"}}, Tunnels: []config.Tunnel{
		{Name: "t1-6", Sides: side("de")},
		{Name: "t1", Over: "t1-6", Sides: side("de")},
		{Name: "t2", Sides: side("de"), Disabled: true},
		{Name: "t3", Sides: side("de"), QuotaGB: 100},
		{Name: "t4", Sides: side("fi")},
		{Name: "t5", Sides: side("de")},
	}}
	var got []string
	for _, x := range nightlyTunnels(st) {
		got = append(got, x.Name)
	}
	if len(got) != 2 || got[0] != "t1" || got[1] != "t5" {
		t.Errorf("tested %v", got)
	}
}
