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
