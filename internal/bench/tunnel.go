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
// ICMP ping otherwise.
func TestTunnel(ctx context.Context, id *identity.Identity, st *config.State, name string, o Options, progress func(string)) (Result, error) {
	t, ok := st.Tunnel(name)
	if !ok {
		return Result{}, fmt.Errorf("unknown tunnel %q (tests run on the server that created it)", name)
	}
	far := 1
	if st.IsSelf(t.Sides[1].Node) {
		far = 0
	}
	n := t.Net4
	if n == "" {
		n = t.Net6
	}
	target, ok := config.Inner(n, far)
	if !ok {
		return Result{}, fmt.Errorf("tunnel %s has no inner address", name)
	}
	res := Result{Name: t.Name, Title: t.Name, Mode: t.Mode, Port: t.Port, MTU: engine.State(engine.Ifname(t.Name)).MTU}
	p, managed := st.Peer(t.Sides[far].Node)
	if !managed || !p.Paired() {
		if progress != nil {
			progress("far end does not run neotunnel: ICMP ping only")
		}
		lat, err := PingICMP(ctx, target.Addr().String(), o.Pings)
		res.Latency = lat
		return res, err
	}
	r := &Runner{ID: id, State: st, Peer: *p, Opts: o, Progress: progress}
	var err error
	res.Latency, res.Up, res.Down, err = r.MeasureTunnel(ctx, t.Name, target.Addr())
	return res, err
}
