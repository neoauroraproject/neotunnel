package failover

import (
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
)

var fw = config.Forward{Node: "ir", Tunnel: "main", Backup: "spare", Proto: "tcp", Ports: "443"}

// rounds feeds n probe rounds of 5 packets, got of which come back.
func rounds(tr *health.Tracker, tunnel string, got, n int) {
	for range n {
		tr.RecordRound(tunnel, 5, got, time.Millisecond)
	}
}

// Healthy and degraded states, as the tracker grades them.
var (
	good  = health.State{Probes: 40, Passes: true, Grade: health.GradeOK, Good: RecoverAfter}
	dead  = health.State{Probes: 40, Passes: false, Grade: health.GradeDown, Loss: 100, Bad: FailAfter}
	lossy = func(loss float64) health.State {
		return health.State{Probes: 40, Passes: true, Grade: health.GradeBad, Loss: loss, Bad: FailAfter}
	}
)

func TestFailsOverOnHeavyLossAndComesBackWithHysteresis(t *testing.T) {
	tr := health.New()
	ps := Paths{}
	now := time.Unix(1_700_000_000, 0)
	step := func() map[string]Switch {
		now = now.Add(8 * time.Second)
		return Step(ps, []config.Forward{fw}, tr.Snapshot(), now)
	}
	rounds(tr, "main", 5, 3)
	rounds(tr, "spare", 5, 3)
	if m := step(); len(m) != 0 || ps.ActiveOf(fw) != "main" {
		t.Fatalf("healthy primary: switched %v, active %s", m, ps.ActiveOf(fw))
	}

	// Rounds that still get one reply in five (80% loss) are bad: the old
	// "any reply passes" rule kept a tunnel like that as working.
	for range FailAfter - 1 {
		rounds(tr, "main", 1, 1)
		rounds(tr, "spare", 5, 1)
		if m := step(); len(m) != 0 {
			t.Fatalf("switched after %d bad rounds", tr.Snapshot()["main"].Bad)
		}
	}
	rounds(tr, "main", 1, 1)
	rounds(tr, "spare", 5, 1)
	m := step()
	if s, ok := m[Key(fw)]; !ok || s.From != "main" || s.To != "spare" {
		t.Fatalf("no failover after %d bad rounds: %v", FailAfter, m)
	}

	// The primary answering again isn't enough until it has done so for a
	// while; a bad round in between starts the count over.
	for range RecoverAfter - 1 {
		rounds(tr, "main", 5, 1)
		step()
	}
	rounds(tr, "main", 0, 1)
	step()
	for range RecoverAfter - 1 {
		rounds(tr, "main", 5, 1)
		if m := step(); len(m) != 0 {
			t.Fatalf("came back before %d good rounds in a row: %v", RecoverAfter, m)
		}
	}
	rounds(tr, "main", 5, 1)
	m = step()
	if s, ok := m[Key(fw)]; !ok || s.To != "main" {
		t.Fatalf("did not come back: %v", m)
	}
	p := ps[Key(fw)]
	if len(p.Switches) != 2 || p.Since != now.Unix() {
		t.Fatalf("history %+v", p)
	}
}

// The case from a real server: GRE at 95% loss, its GENEVE backup at 80%.
// Neither is usable; moving gains nothing, so the forward stays (the daemon
// restarts them instead). A backup clearly better than the primary is
// worth moving to even when it isn't healthy.
func TestMovesOnlyToABetterBackup(t *testing.T) {
	hs := map[string]health.State{"main": lossy(95), "spare": lossy(80)}
	if next, _ := Decide(fw, "main", hs); next != "main" {
		t.Fatalf("moved from 95%% to 80%% loss")
	}
	hs["spare"] = lossy(50)
	if next, why := Decide(fw, "main", hs); next != "spare" || why == "" {
		t.Fatalf("stayed at 95%% loss with a 50%% backup")
	}
	hs["spare"] = health.State{Probes: 40, Passes: true, Grade: health.GradeDegraded, Loss: 8}
	if next, _ := Decide(fw, "main", hs); next != "spare" {
		t.Fatalf("stayed with a merely degraded backup")
	}
	hs["spare"] = dead
	if next, _ := Decide(fw, "main", hs); next != "main" {
		t.Fatalf("moved to a dead backup")
	}
	// Never probed counts as not working.
	if next, _ := Decide(fw, "main", map[string]health.State{"main": dead}); next != "main" {
		t.Fatalf("moved to an unprobed backup")
	}
}

func TestBackupFailingReturnsToBetterPrimary(t *testing.T) {
	hs := map[string]health.State{"main": {Probes: 40, Passes: true, Grade: health.GradeDegraded, Loss: 10}, "spare": dead}
	if next, why := Decide(fw, "spare", hs); next != "main" || why == "" {
		t.Fatalf("stayed on a dead backup: %s %q", next, why)
	}
	hs["main"] = dead
	if next, _ := Decide(fw, "spare", hs); next != "spare" {
		t.Fatalf("left the backup for a primary that doesn't answer")
	}
}

func TestMinStayBetweenSwitches(t *testing.T) {
	ps := Paths{}
	now := time.Unix(1_700_000_000, 0)
	down := map[string]health.State{"main": dead, "spare": good}
	up := map[string]health.State{"main": good, "spare": good}
	if m := Step(ps, []config.Forward{fw}, down, now); len(m) != 1 {
		t.Fatalf("no first switch")
	}
	if m := Step(ps, []config.Forward{fw}, up, now.Add(MinStay/2)); len(m) != 0 {
		t.Fatalf("switched back within %s", MinStay)
	}
	if m := Step(ps, []config.Forward{fw}, up, now.Add(MinStay)); len(m) != 1 {
		t.Fatalf("did not switch back after %s", MinStay)
	}
}

func TestBackupRemovedOrChanged(t *testing.T) {
	ps := Paths{Key(fw): {Active: "spare"}}
	f := fw
	f.Backup = "other"
	hs := map[string]health.State{"main": good, "other": good}
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
	up := map[string]health.State{"main": good, "spare": good}
	down := map[string]health.State{"main": dead, "spare": good}
	now := time.Unix(1_700_000_000, 0)
	for i := range 3 * MaxHistory {
		hs := down
		if i%2 == 1 {
			hs = up
		}
		now = now.Add(MinStay)
		Step(ps, []config.Forward{fw}, hs, now)
	}
	if n := len(ps[Key(fw)].Switches); n != MaxHistory {
		t.Fatalf("%d switches kept", n)
	}
}

func TestSaveLoad(t *testing.T) {
	config.Dir = t.TempDir()
	ps := Paths{}
	Step(ps, []config.Forward{fw}, map[string]health.State{"main": dead, "spare": good}, time.Unix(100, 0))
	if err := Save(ps); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.ActiveOf(fw) != "spare" || got[Key(fw)].Switches[0].At != 100 {
		t.Fatalf("loaded %+v", got[Key(fw)])
	}
}
