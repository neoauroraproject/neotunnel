package failover

import (
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
)

var fw = config.Forward{Node: "ir", Tunnel: "main", Backup: "spare", Proto: "tcp", Ports: "443"}

// probe feeds n results for a tunnel into the tracker.
func probe(tr *health.Tracker, tunnel string, ok bool, n int) {
	for range n {
		tr.Record(tunnel, ok, time.Millisecond)
	}
}

func TestFailsOverAfterLostProbesAndComesBackWithHysteresis(t *testing.T) {
	tr := health.New()
	ps := Paths{}
	now := time.Unix(1_700_000_000, 0)
	step := func() map[string]Switch {
		now = now.Add(8 * time.Second)
		return Step(ps, []config.Forward{fw}, tr.Snapshot(), now)
	}
	probe(tr, "main", true, 5)
	probe(tr, "spare", true, 5)
	if m := step(); len(m) != 0 || ps.ActiveOf(fw) != "main" {
		t.Fatalf("healthy primary: switched %v, active %s", m, ps.ActiveOf(fw))
	}

	// Fewer than FailAfter lost probes keep the forward where it is.
	for range FailAfter - 1 {
		probe(tr, "main", false, 1)
		probe(tr, "spare", true, 1)
		if m := step(); len(m) != 0 {
			t.Fatalf("switched after %d lost probes", tr.Snapshot()["main"].Down)
		}
	}
	probe(tr, "main", false, 1)
	probe(tr, "spare", true, 1)
	m := step()
	if s, ok := m[Key(fw)]; !ok || s.From != "main" || s.To != "spare" {
		t.Fatalf("no failover after %d lost probes: %v", FailAfter, m)
	}
	if ps.ActiveOf(fw) != "spare" {
		t.Fatalf("active %s, want spare", ps.ActiveOf(fw))
	}

	// The primary answering again isn't enough until it has done so for a
	// while; a single loss in between starts the count over.
	probe(tr, "main", true, RecoverAfter-1)
	probe(tr, "main", false, 1)
	probe(tr, "main", true, RecoverAfter-1)
	if m := step(); len(m) != 0 {
		t.Fatalf("came back before %d probes in a row: %v", RecoverAfter, m)
	}
	probe(tr, "main", true, 1)
	m = step()
	if s, ok := m[Key(fw)]; !ok || s.To != "main" {
		t.Fatalf("did not come back: %v", m)
	}
	p := ps[Key(fw)]
	if len(p.Switches) != 2 || p.Since != now.Unix() {
		t.Fatalf("history %+v", p)
	}
}

func TestStaysWhenBackupIsDownToo(t *testing.T) {
	hs := map[string]health.State{"main": {Down: 10}, "spare": {Down: 4}}
	if next, _ := Decide(fw, "main", hs); next != "main" {
		t.Fatalf("moved to a dead backup")
	}
	// Never probed at all counts as not working.
	if next, _ := Decide(fw, "main", map[string]health.State{"main": {Down: 10}}); next != "main" {
		t.Fatalf("moved to an unprobed backup")
	}
}

func TestBackupFailingReturnsToAnsweringPrimary(t *testing.T) {
	hs := map[string]health.State{"main": {Up: 1}, "spare": {Down: FailAfter}}
	if next, why := Decide(fw, "spare", hs); next != "main" || why == "" {
		t.Fatalf("stayed on a failed backup: %s %q", next, why)
	}
	hs["main"] = health.State{Down: 2}
	if next, _ := Decide(fw, "spare", hs); next != "spare" {
		t.Fatalf("left the backup for a primary that doesn't answer")
	}
}

func TestBackupRemovedOrChanged(t *testing.T) {
	ps := Paths{Key(fw): {Active: "spare"}}
	f := fw
	f.Backup = "other"
	hs := map[string]health.State{"main": {Up: 1}, "other": {Up: 1}}
	m := Step(ps, []config.Forward{f}, hs, time.Now())
	if s := m[Key(f)]; s.To != "main" {
		t.Fatalf("stale active tunnel kept: %v", m)
	}
	// A forward without a backup has no path; old paths are dropped.
	ps[Key(fw)].Active = "spare"
	f.Backup = ""
	Step(ps, []config.Forward{f}, hs, time.Now())
	if len(ps) != 0 {
		t.Fatalf("paths left: %v", ps)
	}
}

func TestRoute(t *testing.T) {
	plain := config.Forward{Node: "ir", Tunnel: "main", Ports: "80"}
	ts := []config.Tunnel{{Name: "main"}, {Name: "spare"}}
	out := Route([]config.Forward{fw, plain}, ts, Paths{Key(fw): {Active: "spare"}})
	if out[0].Tunnel != "spare" || out[1].Tunnel != "main" {
		t.Fatalf("route %+v", out)
	}
	if fw.Tunnel != "main" {
		t.Fatal("Route changed its input")
	}
	// A primary turned off sends the forward to the backup right away.
	ts[0].Disabled = true
	if out := Route([]config.Forward{fw}, ts, Paths{}); out[0].Tunnel != "spare" {
		t.Fatalf("disabled primary: %+v", out)
	}
	ts[1].Disabled = true
	if out := Route([]config.Forward{fw}, ts, Paths{}); out[0].Tunnel != "main" {
		t.Fatalf("both off: %+v", out)
	}
}

func TestHistoryIsCapped(t *testing.T) {
	ps := Paths{}
	up := map[string]health.State{"main": {Up: RecoverAfter}, "spare": {Up: 1}}
	down := map[string]health.State{"main": {Down: FailAfter}, "spare": {Up: 1}}
	for i := range 3 * MaxHistory {
		hs := down
		if i%2 == 1 {
			hs = up
		}
		Step(ps, []config.Forward{fw}, hs, time.Now())
	}
	if n := len(ps[Key(fw)].Switches); n != MaxHistory {
		t.Fatalf("%d switches kept", n)
	}
}

func TestSaveLoad(t *testing.T) {
	config.Dir = t.TempDir()
	ps := Paths{}
	Step(ps, []config.Forward{fw}, map[string]health.State{"main": {Down: 5}, "spare": {Up: 2}}, time.Unix(100, 0))
	if err := Save(ps); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.ActiveOf(fw) != "spare" || got[Key(fw)].Switches[0].At != 100 {
		t.Fatalf("loaded %+v", got[Key(fw)])
	}
}
