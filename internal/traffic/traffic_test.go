package traffic

import (
	"testing"
	"time"
)

func store() *Store {
	return &Store{Tunnels: map[string]*Counter{}, path: "", location: time.UTC}
}

func TestDeltasSurviveRecreation(t *testing.T) {
	s := store()
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.Sample("t1", Pair{Rx: 1000, Tx: 500}, t0) // baseline
	s.Sample("t1", Pair{Rx: 3000, Tx: 700}, t0.Add(10*time.Second))
	s.Sample("t1", Pair{Rx: 400, Tx: 100}, t0.Add(20*time.Second)) // interface re-created
	sum := s.Summaries(t0.Add(30 * time.Second))["t1"]
	if sum.Total.Rx != 2400 || sum.Total.Tx != 300 {
		t.Fatalf("total %+v, want rx 2400 tx 300", sum.Total)
	}
	if sum.Today != sum.Total || sum.Month != sum.Total {
		t.Errorf("today %+v month %+v should equal total", sum.Today, sum.Month)
	}
	if last := sum.Series[Buckets-1]; last.Rx != 2400 {
		t.Errorf("current bucket %+v", last)
	}
}

func TestSeriesRollsAndDaysSplit(t *testing.T) {
	s := store()
	t0 := time.Date(2026, 10, 16, 23, 50, 0, 0, time.UTC)
	s.Sample("t", Pair{Rx: 0, Tx: 0}, t0)
	s.Sample("t", Pair{Rx: 100, Tx: 0}, t0.Add(time.Minute))
	s.Sample("t", Pair{Rx: 300, Tx: 0}, t0.Add(20*time.Minute)) // next day, 4 buckets later
	sum := s.Summaries(t0.Add(20 * time.Minute))["t"]
	if sum.Today.Rx != 200 {
		t.Errorf("today %d, want 200", sum.Today.Rx)
	}
	if sum.Series[Buckets-1].Rx != 200 || sum.Series[Buckets-5].Rx != 100 {
		t.Errorf("series tail %+v", sum.Series[Buckets-6:])
	}
	if len(sum.Days) != 30 || sum.Days[28].Rx != 100 || sum.Days[29].Rx != 200 {
		t.Errorf("days tail %+v", sum.Days[27:])
	}
	// An hour of silence later the series has moved on.
	later := s.Summaries(t0.Add(80 * time.Minute))["t"]
	if later.Series[Buckets-1].Rx != 0 || later.Series[Buckets-13].Rx != 200 {
		t.Errorf("series not aligned to now: %+v", later.Series[Buckets-14:])
	}
}

func TestKeepAndReset(t *testing.T) {
	s := store()
	now := time.Now()
	s.Sample("a", Pair{Rx: 0, Tx: 0}, now)
	s.Sample("a", Pair{Rx: 10, Tx: 10}, now)
	s.Sample("b", Pair{Rx: 0, Tx: 0}, now)
	s.Keep([]string{"a"}, now)
	if _, ok := s.Tunnels["b"]; ok {
		t.Error("removed tunnel kept")
	}
	s.Reset("", now)
	if s.Tunnels["a"].Total.Rx != 0 {
		t.Error("reset kept totals")
	}
	s.Sample("a", Pair{Rx: 15, Tx: 10}, now)
	if s.Tunnels["a"].Total.Rx != 5 {
		t.Errorf("after reset only new traffic counts, got %d", s.Tunnels["a"].Total.Rx)
	}
}

func TestSubtractMovesTestTraffic(t *testing.T) {
	s := store()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.Sample("t", Pair{}, now)
	s.Sample("t", Pair{Rx: 5000, Tx: 300, RxP: 10, TxP: 4}, now.Add(10*time.Second))
	s.Subtract("t", Pair{Rx: 4000, Tx: 1000, RxP: 8, TxP: 2}, now.Add(20*time.Second))
	sum := s.Summaries(now.Add(30 * time.Second))["t"]
	if sum.Total != (Pair{Rx: 1000, RxP: 2, TxP: 2}) || sum.Today != sum.Total {
		t.Errorf("after the test: total %+v today %+v", sum.Total, sum.Today)
	}
	if sum.Tests.Rx != 4000 || sum.Series[Buckets-1].Rx != 1000 {
		t.Errorf("tests %+v, last bucket %+v", sum.Tests, sum.Series[Buckets-1])
	}
}
