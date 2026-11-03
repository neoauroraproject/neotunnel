package bench

import (
	"context"
	"fmt"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
)

// TestTunnel measures an existing tunnel defined on this node: full test
// (ping, jitter, loss, both directions) when the far end is a paired peer,
// ICMP ping otherwise. A connection tunnel is measured over its sessions.
func TestTunnel(ctx context.Context, id *identity.Identity, st *config.State, name string, o Options, progress func(string)) (Result, error) {
	t, ok := st.Tunnel(name)
	if !ok {
		return Result{}, fmt.Errorf("unknown tunnel %q (tests run on the server that created it)", name)
	}
	tg, ok := tunnelTarget(st, *t)
	if !ok {
		return Result{}, fmt.Errorf("tunnel %s has no inner address", name)
	}
	far := farSide(st, *t)
	res := Result{Name: t.Name, Title: t.Name, Mode: t.Mode, Port: t.Port}
	if !t.IsStream() {
		res.MTU = engine.State(engine.Ifname(t.Name)).MTU
	}
	p, managed := st.Peer(t.Sides[far].Node)
	if !managed || !p.Paired() {
		if t.IsStream() {
			return res, fmt.Errorf("tunnel %s: its far end is not a connected server", name)
		}
		if progress != nil {
			progress("far end does not run neotunnel: ICMP ping only")
		}
		lat, err := PingICMP(ctx, tg.addr.String(), o.Pings)
		res.Latency = lat
		return res, err
	}
	if t.IsStream() {
		if !p.StreamTests {
			return res, fmt.Errorf("%s runs an older NeoTunnel that can't take speed tests over connection tunnels; update it first", p.Name)
		}
		if !engine.InDaemon() {
			return res, fmt.Errorf("tunnel %s is a connection tunnel: its sessions run inside the NeoTunnel service, so test it from the panel", name)
		}
	}
	r := &Runner{ID: id, State: st, Peer: *p, Opts: o, Progress: progress}
	var err error
	res.Latency, res.Up, res.Down, err = r.measureTunnel(ctx, t.Name, tg)
	return res, err
}
