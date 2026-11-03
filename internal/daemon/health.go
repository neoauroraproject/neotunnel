package daemon

import (
	"context"
	"log"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/events"
	"neotunnel/internal/failover"
	"neotunnel/internal/forward"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
	"neotunnel/internal/transport"
)

const (
	healthEvery = 8 * time.Second
	// roundSize is how many pings a probe round sends, 0.2 s apart: one ping
	// a round only told "something came back", which a tunnel losing 95%
	// of its packets still did now and then.
	roundSize = 5
)

var (
	pingCounts = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
	pingAvg    = regexp.MustCompile(`= [\d.]+/([\d.]+)/`)
)

// pingRound sends a round of pings to addr through the tunnel and returns
// how many were sent and answered, and their average round-trip time. A
// ping that couldn't run at all counts as a round lost.
func pingRound(ctx context.Context, addr string) (sent, got int, rtt time.Duration) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return roundSize, 0, 0
	}
	args := []string{"-c", strconv.Itoa(roundSize), "-i", "0.2", "-W", "1", "-n", "-q"}
	if a.Is6() {
		args = append([]string{"-6"}, args...)
	}
	out, _ := exec.CommandContext(cctx, "ping", append(args, addr)...).Output()
	m := pingCounts.FindSubmatch(out)
	if m == nil {
		return roundSize, 0, 0
	}
	sent, _ = strconv.Atoi(string(m[1]))
	got, _ = strconv.Atoi(string(m[2]))
	if sent == 0 {
		sent = roundSize
	}
	if avg := pingAvg.FindSubmatch(out); avg != nil {
		ms, _ := strconv.ParseFloat(string(avg[1]), 64)
		rtt = time.Duration(ms * float64(time.Millisecond))
	}
	return sent, got, rtt
}

// streamRound pings a connection tunnel's far end roundSize times over its
// sessions, like pingRound.
func streamRound(hub *transport.Hub, name string) (sent, got int, rtt time.Duration) {
	var sum time.Duration
	for i := range roundSize {
		if i > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		d, err := hub.Ping(name, time.Second)
		if err != nil {
			continue
		}
		got++
		sum += d
	}
	if got > 0 {
		rtt = sum / time.Duration(got)
	}
	return roundSize, got, rtt
}

// monitorHealth pings the far inner address of every tunnel and records the
// result, so the panel knows whether traffic really passes, then moves
// forwards with a backup tunnel to whichever of their tunnels works
// (reconcile applies the move).
func monitorHealth(ctx context.Context, id *identity.Identity, tr *health.Tracker, hub *transport.Hub, reconcile func()) {
	rec := newRecovery()
	auto := newAutoCrypt()
	classes := map[string]string{} // last logged health class, by tunnel
	t := time.NewTicker(healthEvery)
	defer t.Stop()
	for {
		st, err := config.Load()
		if err == nil {
			ts, _ := st.Effective()
			keep := map[string]bool{}
			var wg sync.WaitGroup
			for _, tn := range ts {
				if tn.IsStream() {
					// No inner address: the round goes over a session.
					keep[tn.Name] = true
					wg.Add(1)
					go func(name string) {
						defer wg.Done()
						sent, got, rtt := streamRound(hub, name)
						tr.RecordRound(name, sent, got, rtt)
					}(tn.Name)
					continue
				}
				far, ok := farInner(st, tn)
				if !ok {
					continue
				}
				keep[tn.Name] = true
				if !engine.State(engine.Ifname(tn.Name)).Up {
					tr.RecordRound(tn.Name, roundSize, 0, 0)
					continue
				}
				wg.Add(1)
				go func(name, addr string) {
					defer wg.Done()
					sent, got, rtt := pingRound(ctx, addr)
					tr.RecordRound(name, sent, got, rtt)
				}(tn.Name, far)
			}
			wg.Wait()
			tr.Forget(keep)
			logHealth(tr.Snapshot(), classes)
			// Forwards first: with a backup that works, not being cut off
			// comes before fixing the tunnel. They move as soon as the tunnel
			// fails; its restart and encryption steps follow behind.
			if failoverStep(st, tr.Snapshot()) {
				reconcile()
			}
			rec.step(ctx, id, st, tr)
			auto.step(ctx, id, st, tr, reconcile)
			tr.Save()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// failoverStep moves this node's forwards between their primary and backup
// tunnels as the health probes say, and reports whether any moved.
func failoverStep(st *config.State, hs map[string]health.State) bool {
	_, fs := st.Effective()
	ps := failover.Load()
	before := len(ps)
	made := failover.Step(ps, fs, hs, time.Now())
	for k, s := range made {
		log.Printf("forward %s: now through %s (%s)", k, s.To, s.Reason)
		if s.Better {
			for _, f := range fs {
				if failover.Key(f) == k {
					forward.KeepFlows(f.PortKey())
				}
			}
		}
		ports := k[strings.IndexByte(k, ' ')+1:]
		for _, t := range []string{s.From, s.To} {
			events.Add(events.Event{Tunnel: t, Kind: events.Failover, From: s.From, To: s.To, Detail: ports + "|" + s.Reason, Auto: true})
		}
	}
	if len(made) > 0 || len(ps) != before {
		if err := failover.Save(ps); err != nil {
			log.Printf("failover: %v", err)
		}
	}
	return len(made) > 0
}

// farInner is the inner address of the tunnel's far end, the thing to ping.
func farInner(st *config.State, t config.Tunnel) (string, bool) {
	me := 0
	if st.IsSelf(t.Sides[1].Node) {
		me = 1
	}
	n := t.Net4
	if n == "" {
		n = t.Net6
	}
	a, ok := config.Inner(n, 1-me)
	if !ok {
		return "", false
	}
	return a.Addr().String(), true
}

// logHealth records a tunnel's health moving between working (ok or
// degraded), bad and down. A tunnel's first state after the daemon starts is
// taken as it is, once enough probes back it.
func logHealth(hs map[string]health.State, classes map[string]string) {
	for name, h := range hs {
		c := events.Class(h.Grade)
		if c == "" || h.Probes < 2*roundSize {
			continue
		}
		prev, seen := classes[name]
		classes[name] = c
		if seen && prev != c {
			events.Add(events.Event{Tunnel: name, Kind: events.Health, From: prev, To: c, Loss: h.Loss})
		}
	}
}
