package daemon

import (
	"math"
	"strings"
	"time"

	"neotunnel/internal/metrics"
	"neotunnel/internal/traffic"
)

// history turns one round of counter readings into graph samples: rates
// from the change since the previous round, plus CPU, memory and load.
// The first reading of a counter (or one that went backwards, after the
// interface was re-created) only sets the baseline.
type history struct {
	ms   *metrics.Store
	cpu  metrics.CPU
	last map[string]traffic.Pair
	prev time.Time
	now  time.Time
	dt   time.Duration
	sum  [2]float64 // all tunnels that carry traffic of their own
	any  bool
}

func newHistory(ms *metrics.Store) *history {
	h := &history{ms: ms, last: map[string]traffic.Pair{}}
	h.cpu.Read()
	return h
}

// begin starts a round taken at now.
func (h *history) begin(now time.Time) {
	h.now, h.dt = now, now.Sub(h.prev)
	if h.prev.IsZero() || h.dt > 3*sampleEvery {
		h.dt = 0 // first round, or the clock jumped: rates would be wrong
	}
	h.prev = now
	h.sum, h.any = [2]float64{}, false
}

// rate records the bits per second of a counter since the last round; in
// counts it toward the total of all tunnels.
func (h *history) rate(key string, c traffic.Pair, total bool) {
	p, seen := h.last[key]
	h.last[key] = c
	if !seen || h.dt <= 0 || c.Rx < p.Rx || c.Tx < p.Tx {
		return
	}
	sec := h.dt.Seconds()
	rx, tx := float64(c.Rx-p.Rx)*8/sec, float64(c.Tx-p.Tx)*8/sec
	h.ms.Record(key, h.now, h.dt, rx, tx)
	if total {
		h.sum[0] += rx
		h.sum[1] += tx
		h.any = true
	}
}

// record stores a reading as it is (ping and loss).
func (h *history) record(key string, a, b float64) {
	if h.dt > 0 {
		h.ms.Record(key, h.now, h.dt, a, b)
	}
}

// keep forgets counters and history of tunnels and ports that are gone.
func (h *history) keep(names []string) {
	keepNames := map[string]bool{}
	for _, n := range names {
		keepNames[strings.TrimPrefix(n, traffic.Port)] = true
	}
	for k := range h.last {
		if i := strings.IndexByte(k, ':'); i > 0 && !keepNames[k[i+1:]] {
			delete(h.last, k)
		}
	}
	h.ms.Keep(keepNames)
}

// end records the round's totals and the system readings.
func (h *history) end() {
	if h.dt <= 0 {
		h.cpu.Read()
		return
	}
	if h.any {
		h.ms.Record("all", h.now, h.dt, h.sum[0], h.sum[1])
	}
	if total, hot, ok := h.cpu.Read(); ok {
		h.ms.Record("cpu", h.now, h.dt, total, hot)
	}
	if mem, swap, ok := metrics.Memory(); ok {
		h.ms.Record("mem", h.now, h.dt, mem, swap)
	}
	if l, ok := metrics.Load(); ok {
		h.ms.Record("load", h.now, h.dt, l, math.NaN())
	}
}
