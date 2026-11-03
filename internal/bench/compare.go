package bench

import (
	"context"
	"fmt"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/probe"
)

// Status of a candidate while a comparison runs.
const (
	StatusPending   = "pending"
	StatusChecking  = "checking"
	StatusPassed    = "passed"
	StatusMeasuring = "measuring"
	StatusDone      = "done"
	StatusFailed    = "failed"
	// StatusSkipped: not tested, e.g. an existing tunnel already holds the
	// addresses this kind needs.
	StatusSkipped = "skipped"
)

// conflictKey groups candidates the kernel can't run side by side: keyless
// SIT/IPIP/ip6tnl tunnels between the same two addresses, with or without a
// UDP (FOU) port, since the kernel tells them apart by addresses only.
func conflictKey(c Candidate) string {
	b := c.Tunnels[0]
	switch b.Kind {
	case config.KindSIT, config.KindIPIP:
		return string(b.Kind)
	case config.KindIPIP6, config.KindIP6IP6:
		return "ip6tnl"
	}
	return ""
}

// waves splits candidates into groups that can be up at the same time.
func waves(cs []Candidate) [][]Candidate {
	var out [][]Candidate
	var keys []map[string]bool
	for _, c := range cs {
		k := conflictKey(c)
		placed := false
		for i := range out {
			if k == "" || !keys[i][k] {
				out[i] = append(out[i], c)
				if k != "" {
					keys[i][k] = true
				}
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, []Candidate{c})
			keys = append(keys, map[string]bool{k: k != ""})
		}
	}
	return out
}

// Compare tests candidates in two phases. First every candidate is brought
// up and checked for connectivity, a whole wave at a time and in parallel,
// so a filtered protocol costs a few seconds instead of a full test. Then
// bandwidth is measured, one at a time, only for the candidates that
// passed. report receives every status change, so a panel can show it live.
func (r *Runner) Compare(ctx context.Context, cs []Candidate, report func(Result)) []Result {
	results := make([]Result, len(cs))
	var mu sync.Mutex
	set := func(i int, res Result) {
		mu.Lock()
		results[i] = res
		mu.Unlock()
		if report != nil {
			report(res)
		}
	}
	index := map[string]int{}
	for i, c := range cs {
		index[c.Name] = i
		set(i, Result{Name: c.Name, Title: c.Title, Mode: c.Mode, Port: c.Port, Status: StatusPending})
	}
	if err := r.open(ctx); err != nil {
		for i := range cs {
			res := results[i]
			res.Status, res.Err = StatusFailed, err.Error()
			set(i, res)
		}
		return results
	}

	// Candidates an existing tunnel stands in for are measured directly.
	var todo []Candidate
	for _, c := range cs {
		t, err := r.existing(c)
		i := index[c.Name]
		res := results[i]
		switch {
		case err != nil:
			res.Status, res.Err = StatusSkipped, err.Error()
			set(i, res)
		case t == nil && needsDaemon(c):
			res.Status, res.Err = StatusSkipped, engine.ErrNeedsDaemon.Error()
			set(i, res)
		case t == nil && c.stream() && !r.Peer.StreamTests:
			res.Status, res.Err = StatusSkipped, "skipped: "+errOldPeer(r.Peer.Name).Error()
			set(i, res)
		case t != nil:
			res.Title += " [" + t.Name + "]"
			res.MTU = engine.State(engine.Ifname(t.Name)).MTU
			res.Status = StatusMeasuring
			set(i, res)
			tg, _ := tunnelTarget(r.State, *t)
			if r.CheckOnly {
				lat, err := r.quickCheck(ctx, tg)
				res.Latency, res.Status = lat, StatusPassed
				if err != nil {
					res.Status, res.Err = StatusFailed, err.Error()
				}
				set(i, res)
				continue
			}
			lat, up, down, err := r.measureTunnel(ctx, t.Name, tg)
			res.Latency, res.Up, res.Down = lat, up, down
			res.Status = StatusDone
			if err != nil {
				res.Status, res.Err = StatusFailed, err.Error()
			}
			set(i, res)
		default:
			todo = append(todo, c)
		}
	}

	for _, wave := range waves(todo) {
		if ctx.Err() != nil {
			break
		}
		r.runWave(ctx, wave, index, results, set)
	}
	return results
}

func (r *Runner) runWave(ctx context.Context, wave []Candidate, index map[string]int, results []Result, set func(int, Result)) {
	for _, c := range wave {
		i := index[c.Name]
		res := results[i]
		res.Status = StatusChecking
		set(i, res)
	}
	fail := func(c Candidate, err string) {
		i := index[c.Name]
		res := results[i]
		res.Status, res.Err = StatusFailed, err
		set(i, res)
	}

	// Bring the wave up on both ends, each candidate on its own so a kind
	// a kernel lacks fails alone.
	var (
		upMu      sync.Mutex
		upWG      sync.WaitGroup
		peerNames []string
		names     []string
		ready     []Candidate
	)
	mtu := map[string]int{}
	defer func() {
		engine.RemoveBench(names)
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.client.BenchDown(cctx, peerNames)
	}()
	for _, c := range wave {
		upWG.Add(1)
		go func(c Candidate) {
			defer upWG.Done()
			up, err := r.bringUp(ctx, c, 300*time.Second)
			upMu.Lock()
			defer upMu.Unlock()
			if err != nil {
				fail(c, err.Error())
				return
			}
			peerNames = append(peerNames, up.peer...)
			names = append(names, up.local...)
			mtu[c.Name] = up.mtu
			if up.c.Port != c.Port {
				i := index[c.Name]
				res := results[i]
				res.Title, res.Port, res.Moved = up.c.Title, up.c.Port, c.Port
				set(i, res)
			}
			ready = append(ready, up.c)
		}(c)
	}
	upWG.Wait()

	// Phase 1: connectivity for the whole wave at once.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var passed []Candidate
	for _, c := range ready {
		wg.Add(1)
		go func(c Candidate) {
			defer wg.Done()
			i := index[c.Name]
			res := results[i]
			res.MTU = mtu[c.Name]
			lat, err := r.quickCheck(ctx, candidateTarget(c))
			res.Latency = lat
			if err != nil {
				res.Status, res.Err = StatusFailed, err.Error()
				set(i, res)
				return
			}
			res.Status = StatusPassed
			set(i, res)
			mu.Lock()
			passed = append(passed, c)
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	if r.CheckOnly {
		return
	}

	// Phase 2: bandwidth, one candidate at a time so they don't compete.
	for _, c := range passed {
		if ctx.Err() != nil {
			return
		}
		i := index[c.Name]
		res := results[i]
		res.Status = StatusMeasuring
		set(i, res)
		r.say("%s", c.Title)
		t := candidateTarget(c)
		up, err := r.bandwidth(ctx, t, probe.Upload)
		if err == nil {
			res.Up = up
			var down float64
			down, err = r.bandwidth(ctx, t, probe.Download)
			res.Down = down
		}
		res.Status = StatusDone
		if err != nil {
			res.Status, res.Err = StatusFailed, err.Error()
		}
		set(i, res)
	}
}

// candidateTarget is where a candidate's probes go: the far inner address
// of its top tunnel, or the sessions of its connection tunnel.
func candidateTarget(c Candidate) target {
	last := c.Tunnels[len(c.Tunnels)-1]
	if last.IsStream() {
		return target{stream: streamName(last.Name)}
	}
	n := last.Net4
	if n == "" {
		n = last.Net6
	}
	a, _ := config.Inner(n, 1)
	return target{addr: a.Addr()}
}

// quickCheck waits up to a few seconds for the first reply, then measures
// latency, jitter and loss with a short burst.
func (r *Runner) quickCheck(ctx context.Context, t target) (probe.LatencyResult, error) {
	deadline := time.Now().Add(waitFor(t, 5*time.Second))
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if r.reaches(ctx, t) {
			return r.latency(ctx, t, 30, 20*time.Millisecond)
		}
	}
	return probe.LatencyResult{}, notPassing(t)
}

// stream reports whether c is a connection tunnel, measured over its
// sessions.
func (c Candidate) stream() bool { return c.Tunnels[len(c.Tunnels)-1].IsStream() }

func errOldPeer(peer string) error {
	return fmt.Errorf("%s runs an older NeoTunnel that can't test connection tunnels; update it first", peer)
}
