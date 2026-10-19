package metrics

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryAveragesPeaksAndGaps(t *testing.T) {
	s := &Store{series: map[string]*Series{}}
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	// 10 minutes at 100 Mbit/s with one 10-second burst at 900, then a
	// 10-minute gap, then 5 minutes at 50.
	for i := 0; i < 60; i++ {
		v := 100e6
		if i == 30 {
			v = 900e6
		}
		s.Record("t:a", t0.Add(time.Duration(i)*Every), Every, v, v/2)
	}
	for i := 120; i < 150; i++ {
		s.Record("t:a", t0.Add(time.Duration(i)*Every), Every, 50e6, math.NaN())
	}
	now := t0.Add(149 * Every)

	v := s.Query("1h", []string{"t:a"}, now)
	if v.Step != 10 || v.Points != 360 {
		t.Fatalf("1h view: step %d, %d points", v.Step, v.Points)
	}
	l := v.Series["t:a"]
	last := len(l.A) - 1
	if l.A[last] != 50e6 || !math.IsNaN(l.B[last]) {
		t.Fatalf("newest point: %v / %v", l.A[last], l.B[last])
	}
	if !math.IsNaN(l.A[last-40]) {
		t.Fatalf("the gap should be empty, got %v", l.A[last-40])
	}
	if l.Peak[0] != 900e6 {
		t.Fatalf("peak %v", l.Peak[0])
	}

	// One-minute points average the burst away but keep its peak.
	v = s.Query("24h", []string{"t:a"}, now)
	if v.Step != 120 { // 1440 minutes merged two by two
		t.Fatalf("24h step %d", v.Step)
	}
	l = v.Series["t:a"]
	var peak float64
	for _, x := range l.PA {
		if !math.IsNaN(x) && x > peak {
			peak = x
		}
	}
	if peak != 900e6 {
		t.Fatalf("24h peak %v", peak)
	}
	// Bytes over the range: 59 samples at 100, one at 900, 30 at 50 (bits).
	want := (59*100e6 + 900e6 + 30*50e6) * 10
	if math.Abs(l.Sum[0]-want)/want > 1e-3 {
		t.Fatalf("sum %v, want %v", l.Sum[0], want)
	}
}

func TestSaveAndOpen(t *testing.T) {
	dir := t.TempDir()
	s := &Store{series: map[string]*Series{}, path: filepath.Join(dir, "m.gob.gz")}
	now := time.Now()
	s.Record("cpu", now, Every, 42, 99)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	o := openAt(s.path)
	l := o.Query("1h", []string{"cpu"}, now).Series["cpu"]
	if got := l.A[len(l.A)-1]; got != 42 {
		t.Fatalf("after reload: %v", got)
	}
}

func TestKeepDropsRemovedTunnels(t *testing.T) {
	s := &Store{series: map[string]*Series{}}
	now := time.Now()
	for _, k := range []string{"cpu", "t:a", "t:b", "p:a", "f:tcp/443"} {
		s.Record(k, now, Every, 1, 1)
	}
	s.Keep(map[string]bool{"a": true, "tcp/443": true})
	got := s.Keys()
	want := []string{"cpu", "f:tcp/443", "p:a", "t:a"}
	if len(got) != len(want) {
		t.Fatalf("kept %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v", got)
		}
	}
}

func TestParseStat(t *testing.T) {
	a := parseStat("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\n")
	b := parseStat("cpu  200 0 150 850 0 0 50 0 0 0\ncpu0 150 0 50 400 0 0 50 0 0 0\ncpu1 50 0 100 450 0 0 0 0 0 0\n")
	if a["cpu"] != [2]uint64{200, 1000} {
		t.Fatalf("total line %v", a["cpu"])
	}
	// cpu0 went from 100/500 busy to 250/650: 150 of 150 jiffies, 100%.
	if b["cpu0"][0]-a["cpu0"][0] != 150 || b["cpu0"][1]-a["cpu0"][1] != 150 {
		t.Fatalf("cpu0 %v -> %v", a["cpu0"], b["cpu0"])
	}
}
