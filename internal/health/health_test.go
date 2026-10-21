package health

import (
	"testing"
	"time"
)

func TestRollingLossAndAvg(t *testing.T) {
	tr := New()
	for i := 0; i < 10; i++ {
		tr.Record("t1", true, 20*time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		tr.Record("t1", false, 0)
	}
	s := tr.Snapshot()["t1"]
	if s.Probes != 12 || s.Passes != true {
		t.Fatalf("probes %d passes %v", s.Probes, s.Passes)
	}
	if l := s.Loss; l < 16 || l > 17 {
		t.Errorf("loss %.1f, want ~16.7", l)
	}
	if s.AvgMs != 20 {
		t.Errorf("avg %.1f, want 20", s.AvgMs)
	}
	if s.RTTms != 0 {
		t.Errorf("last probe was lost, rtt should be 0, got %.1f", s.RTTms)
	}
}

func TestAllLostDoesNotPass(t *testing.T) {
	tr := New()
	for i := 0; i < Window; i++ {
		tr.Record("x", false, 0)
	}
	s := tr.Snapshot()["x"]
	if s.Passes || s.Loss != 100 {
		t.Errorf("all lost: passes %v loss %.0f", s.Passes, s.Loss)
	}
	tr.Forget(map[string]bool{})
	if len(tr.Snapshot()) != 0 {
		t.Error("forget kept a tunnel")
	}
}

func TestWindowCaps(t *testing.T) {
	tr := New()
	for i := 0; i < Window*3; i++ {
		tr.Record("t", i%2 == 0, 10*time.Millisecond)
	}
	if s := tr.Snapshot()["t"]; s.Probes != Window {
		t.Errorf("window should cap at %d, got %d", Window, s.Probes)
	}
}

func TestGradesByRounds(t *testing.T) {
	tr := New()
	for range 4 {
		tr.RecordRound("t", 5, 5, 10*time.Millisecond)
	}
	if s := tr.Snapshot()["t"]; s.Grade != GradeOK || s.Good != 4 || s.Bad != 0 {
		t.Fatalf("clean rounds: %+v", s)
	}
	// One reply in five: packets "pass", which the old rule called up.
	tr.RecordRound("t", 5, 1, 10*time.Millisecond)
	if s := tr.Snapshot()["t"]; s.Grade != GradeDegraded || s.Bad != 1 || s.Good != 0 || s.LastLoss != 80 {
		t.Fatalf("one bad round: %+v", s)
	}
	tr.RecordRound("t", 5, 1, 10*time.Millisecond)
	if s := tr.Snapshot()["t"]; s.Grade != GradeBad || s.Bad != 2 || !s.Passes {
		t.Fatalf("two bad rounds: %+v", s)
	}
	// A round in between (20% lost) is neither and starts both counts over.
	tr.RecordRound("t", 5, 4, 10*time.Millisecond)
	if s := tr.Snapshot()["t"]; s.Bad != 0 || s.Good != 0 {
		t.Fatalf("middling round kept a streak: %+v", s)
	}
	tr.Fresh("t")
	tr.Note("t", "restarted")
	if s := tr.Snapshot()["t"]; s.Action != "restarted" || s.ActionAt == 0 {
		t.Fatalf("note: %+v", s)
	}
	for range Window / 5 {
		tr.RecordRound("dead", 5, 0, 0)
	}
	if s := tr.Snapshot()["dead"]; s.Grade != GradeDown || s.Passes || s.Loss != 100 {
		t.Fatalf("dead tunnel: %+v", s)
	}
}

func TestQuiet(t *testing.T) {
	quietFile = t.TempDir() + "/quiet"
	if Quiet() {
		t.Fatal("quiet without a test")
	}
	QuietFor(time.Minute)
	if !Quiet() {
		t.Fatal("not quiet during a test")
	}
	QuietFor(time.Second) // a shorter test doesn't cut a longer one short
	if quietUntil() < time.Now().Add(50*time.Second).Unix() {
		t.Fatal("a shorter quiet period replaced a longer one")
	}
}
