package daemon

import (
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
	"neotunnel/internal/traffic"
)

// Ports close when paused, past their end or out of quota, and open again
// when a new period starts.
func TestEnforcePlans(t *testing.T) {
	defer forward.SetClosed(nil)
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	store := traffic.Open()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour).Unix()
	fs := []config.Forward{
		{Tunnel: "t1", Proto: "tcp", Ports: "1001", Plan: &config.PortPlan{Start: start, Paused: true}},
		{Tunnel: "t1", Proto: "tcp", Ports: "1002", Plan: &config.PortPlan{Start: start, Expires: now.Unix() - 1}},
		{Tunnel: "t1", Proto: "tcp", Ports: "1003", Plan: &config.PortPlan{Start: start, QuotaGB: 1, ResetDays: 1}},
		{Tunnel: "t1", Proto: "tcp", Ports: "1004", Plan: &config.PortPlan{Start: start, QuotaGB: 1, Expires: now.Unix() + 60}},
	}
	q := traffic.Port + "tcp/1003"
	changes := 0
	enforcePlans(fs, store, now, func() { changes++ })
	store.Sample(q, traffic.Pair{}, now)
	store.Sample(q, traffic.Pair{Rx: 1 << 30}, now.Add(time.Second))
	enforcePlans(fs, store, now.Add(time.Second), func() { changes++ })
	for port, want := range map[string]string{"1001": forward.ClosedPaused, "1002": forward.ClosedExpired, "1003": forward.ClosedQuota, "1004": ""} {
		if got := forward.Closed("tcp/" + port); got != want {
			t.Errorf("port %s: %q, want %q", port, got, want)
		}
	}
	if changes != 2 {
		t.Errorf("%d reconciles, want 2", changes)
	}
	// A day after the start the quota is whole again.
	enforcePlans(fs, store, time.Unix(start, 0).Add(24*time.Hour), func() { changes++ })
	if got := forward.Closed("tcp/1003"); got != "" {
		t.Errorf("new period: %q", got)
	}
}
