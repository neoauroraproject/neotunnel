package daemon

import (
	"testing"
	"time"

	"neotunnel/internal/config"
)

func TestUpgradeWanted(t *testing.T) {
	now := time.Now()
	p := config.Peer{Name: "de", PublicKey: "k", Version: "v0.8.15", LastSeen: now.Add(-time.Minute)}
	if !upgradeWanted(p, "v0.8.16", now) {
		t.Error("an older, reachable peer is not upgraded")
	}
	for name, q := range map[string]config.Peer{
		"same version":     {Name: "de", PublicKey: "k", Version: "v0.8.16", LastSeen: now},
		"newer peer":       {Name: "de", PublicKey: "k", Version: "v0.9.0", LastSeen: now},
		"not paired":       {Name: "de", Version: "v0.8.15", LastSeen: now},
		"unreachable":      {Name: "de", PublicKey: "k", Version: "v0.8.15", LastSeen: now.Add(-time.Hour)},
		"rolled back from": {Name: "de", PublicKey: "k", Version: "v0.8.15", LastSeen: now, RolledBack: "v0.8.16"},
	} {
		if upgradeWanted(q, "v0.8.16", now) {
			t.Errorf("%s: upgraded", name)
		}
	}
	if upgradeWanted(p, "dev", now) {
		t.Error("a dev build pushed to a peer")
	}
}
