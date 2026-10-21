package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
)

func TestRecoveryRestartsOncePerGap(t *testing.T) {
	config.Dir = t.TempDir()
	ends := [2]config.Side{{Node: "ir"}, {Node: "de"}}
	st := &config.State{Self: "ir", Tunnels: []config.Tunnel{
		{Name: "carrier", Kind: config.KindSIT, Sides: ends},
		{Name: "top", Kind: config.KindIPIP6, Over: "carrier", Sides: ends},
		{Name: "theirs", Kind: config.KindGRE, MasterKey: "abc", Sides: ends},
		{Name: "fine", Kind: config.KindGRE, Sides: ends},
	}}
	tr := health.New()
	bad := func(n int) {
		for _, name := range []string{"carrier", "top", "theirs"} {
			for range n {
				tr.RecordRound(name, 5, 0, 0)
			}
		}
		tr.RecordRound("fine", 5, 5, time.Millisecond)
	}
	var mu sync.Mutex
	var got []string
	done := make(chan struct{}, 4)
	r := newRecovery()
	r.restart = func(_ context.Context, _ *identity.Identity, name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		done <- struct{}{}
		return nil
	}

	bad(restartAfter - 1)
	r.step(context.Background(), nil, st, tr)
	bad(1)
	r.step(context.Background(), nil, st, tr)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart after enough bad rounds")
	}
	mu.Lock()
	if len(got) != 1 || got[0] != "top" {
		t.Fatalf("restarted %v, want only the top of the stack", got)
	}
	mu.Unlock()
	if s := tr.Snapshot()["carrier"]; s.Bad != 0 {
		t.Fatalf("carrier's count not started over: %+v", s)
	}
	waitNote(t, tr, "top")

	// Still bad right after: not restarted again within the gap.
	bad(restartAfter)
	r.step(context.Background(), nil, st, tr)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(got) != 1 {
		t.Fatalf("restarted again within %s: %v", restartGap, got)
	}
	mu.Unlock()

	// Turned off in the settings: nothing.
	r.last = map[string]time.Time{}
	st.Defaults = &config.Defaults{NoRecover: true}
	r.step(context.Background(), nil, st, tr)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("restarted with recovery off: %v", got)
	}
}

func waitNote(t *testing.T, tr *health.Tracker, name string) {
	t.Helper()
	for range 100 {
		if tr.Snapshot()[name].Action != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no action noted for %s", name)
}
