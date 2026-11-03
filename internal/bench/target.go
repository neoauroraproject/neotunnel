package bench

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/probe"
)

// target is where a test's probes go: the far server's probe port at an
// address (inside a tunnel, or the direct path), or over a connection
// tunnel's sessions, which have no inner address: each probe is a stream
// that the far end carries to its own probe server.
type target struct {
	addr   netip.Addr
	stream string // the connection tunnel's name here
}

// tunnelTarget is where probes reach the far end of t, a tunnel defined
// here; ok is false when it has no inner address.
func tunnelTarget(st *config.State, t config.Tunnel) (tg target, ok bool) {
	if t.IsStream() {
		return target{stream: t.Name}, true
	}
	n := t.Net4
	if n == "" {
		n = t.Net6
	}
	a, ok := config.Inner(n, farSide(st, t))
	return target{addr: a.Addr()}, ok
}

// farSide is the index of t's side that isn't this server.
func farSide(st *config.State, t config.Tunnel) int {
	if st.IsSelf(t.Sides[1].Node) {
		return 0
	}
	return 1
}

func (t target) hostPort() string {
	return net.JoinHostPort(t.addr.String(), strconv.Itoa(probe.DefaultPort))
}

// streamPingTimeout is how long one ping over a session waits for its echo.
const streamPingTimeout = 2 * time.Second

// latency sends count probes interval apart. A connection tunnel carries no
// UDP, so there each probe is a ping over a session, one at a time.
func (r *Runner) latency(ctx context.Context, t target, count int, interval time.Duration) (probe.LatencyResult, error) {
	if t.stream != "" {
		ping := func() (time.Duration, error) { return engine.StreamPing(t.stream, streamPingTimeout) }
		return probe.PingLatency(ctx, ping, count, interval)
	}
	return probe.Latency(ctx, t.hostPort(), r.token, count, interval, 64)
}

// bandwidth measures one direction for the run's length on its connections.
func (r *Runner) bandwidth(ctx context.Context, t target, dir probe.Direction) (float64, error) {
	phase := "upload"
	if dir == probe.Download {
		phase = "download"
	}
	live := func(bps float64) { r.live(phase, bps) }
	if t.stream != "" {
		dial := func(context.Context) (net.Conn, error) { return engine.StreamProbe(t.stream) }
		return probe.BandwidthOver(ctx, dial, r.token, dir, r.Opts.Duration, r.Opts.Streams, live)
	}
	return probe.BandwidthLive(ctx, t.hostPort(), r.token, dir, r.Opts.Duration, r.Opts.Streams, live)
}

// reaches reports whether a probe gets an answer within moments. On a
// connection tunnel one try also waits a few seconds for a session.
func (r *Runner) reaches(ctx context.Context, t target) bool {
	if t.stream != "" {
		if _, err := engine.StreamPing(t.stream, time.Second); err != nil {
			sleep(ctx, 200*time.Millisecond)
			return false
		}
		return true
	}
	l, err := probe.Latency(ctx, t.hostPort(), r.token, 3, 50*time.Millisecond, 64)
	return err == nil && l.Received > 0
}

// waitFor is how long a new tunnel gets to pass its first probe: a
// connection tunnel's sessions connect and handshake first.
func waitFor(t target, d time.Duration) time.Duration {
	if t.stream != "" {
		return d + 7*time.Second
	}
	return d
}

// notPassing is the error of a tunnel whose probes don't get through.
func notPassing(t target) error {
	if t.stream != "" {
		return fmt.Errorf("no session connects on this path (port filtered, or the far server unreachable?)")
	}
	return fmt.Errorf("no traffic passes on this path")
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// streamName is a temporary connection tunnel's name on both servers
// (engine.ApplyStreamBench starts it under that name).
func streamName(tunnel string) string { return agent.BenchIfname(tunnel) }
