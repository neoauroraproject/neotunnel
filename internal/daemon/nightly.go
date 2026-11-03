package daemon

import (
	"context"
	"log"
	"time"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/failover"
	"neotunnel/internal/identity"
	"neotunnel/internal/speedlog"
)

// The nightly test: once a night, at a quiet hour in Iran, every tunnel this
// server made gets a light speed test (a few seconds each way, kept out of
// the traffic totals). The results are its record night after night, and
// what the best path uses to tell apart tunnels that ping and lose alike.

// nightAt is when the test runs, Tehran time (UTC+3:30, no daylight saving).
const nightAt = 4*time.Hour + 30*time.Minute

var tehranTime = time.FixedZone("IRST", 3*3600+1800)

// nextNight is the next nightly run after now.
func nextNight(now time.Time) time.Time {
	t := now.In(tehranTime)
	at := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tehranTime).Add(nightAt)
	if !at.After(t) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

func nightly(ctx context.Context, id *identity.Identity) {
	failover.SetSpeeds(speedlog.Latest())
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextNight(time.Now()))):
		}
		st, err := config.Load()
		if err != nil || st.D().NoNightly {
			continue
		}
		testAll(ctx, id, st)
		failover.SetSpeeds(speedlog.Latest())
	}
}

// nightlyTunnels are the tunnels to test: this server's own, those a user
// sees (not the layers under them), on, with a paired far server, and
// without a monthly quota (a test would eat into it).
func nightlyTunnels(st *config.State) []config.Tunnel {
	under := map[string]bool{}
	for _, t := range st.Tunnels {
		under[t.Over] = true
	}
	var out []config.Tunnel
	for _, t := range st.Tunnels {
		if under[t.Name] || t.Disabled || t.QuotaGB > 0 || t.IsStream() {
			continue
		}
		far := t.Sides[1].Node
		if st.IsSelf(far) {
			far = t.Sides[0].Node
		}
		if p, ok := st.Peer(far); !ok || !p.Paired() {
			continue
		}
		out = append(out, t)
	}
	return out
}

func testAll(ctx context.Context, id *identity.Identity, st *config.State) {
	ts := nightlyTunnels(st)
	names := make([]string, 0, len(st.Tunnels))
	for _, t := range st.Tunnels {
		names = append(names, t.Name)
	}
	o := bench.Options{Pings: 30, Interval: 20 * time.Millisecond, Duration: 4 * time.Second, Streams: 4}
	for _, t := range ts {
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		r, err := bench.TestTunnel(tctx, id, st, t.Name, o, func(string) {})
		cancel()
		e := speedlog.Entry{At: time.Now().Unix(), Down: r.Down, Up: r.Up, PingMs: float64(r.Latency.Avg.Microseconds()) / 1000, Loss: r.Latency.Loss()}
		if err != nil {
			e.Err = err.Error()
		} else if r.Err != "" {
			e.Err = r.Err
		}
		if err := speedlog.Add(t.Name, e, names); err != nil {
			log.Printf("nightly test of %s: %v", t.Name, err)
		}
		if e.Err != "" {
			log.Printf("nightly test of %s: %s", t.Name, e.Err)
		} else {
			log.Printf("nightly test of %s: down %.0f Mbit/s, up %.0f Mbit/s, ping %.0f ms", t.Name, e.Down/1e6, e.Up/1e6, e.PingMs)
		}
		if ctx.Err() != nil {
			return
		}
	}
}
