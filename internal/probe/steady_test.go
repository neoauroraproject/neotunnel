package probe

import (
	"testing"
	"time"
)

func TestSteadyRateLeavesOutTheRamp(t *testing.T) {
	t0 := time.Unix(1000, 0)
	// 1 MB in the first second (slow start), then 10 MB/s for four seconds.
	s := []sample{{0, t0}, {1e6, t0.Add(time.Second)}}
	for i := 1; i <= 4; i++ {
		s = append(s, sample{1e6 + int64(i)*10e6, t0.Add(time.Duration(1+i) * time.Second)})
	}
	got, ok := steadyRate(s, t0.Add(Warmup(5*time.Second)))
	if !ok || got != 80e6 {
		t.Fatalf("steady rate %v (%v), want 80 Mbit/s", got, ok)
	}
	if _, ok := steadyRate(s[:2], t0.Add(time.Second)); ok {
		t.Fatal("a span under a second gave a rate")
	}
	if Warmup(10*time.Second) != 2500*time.Millisecond || Warmup(30*time.Second) != 3*time.Second {
		t.Fatal("warm-up is a quarter of the run, at most 3 s")
	}
}
