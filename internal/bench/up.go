package bench

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
)

// up is a candidate running on both ends: the interfaces to remove after
// the test, here and on the peer, and the candidate as it really runs (on
// another port if its own was taken).
type up struct {
	c     Candidate
	peer  []string
	local []string
	mtu   int
}

// portTries is how many ports a candidate tries before its test fails.
const portTries = 4

// bringUp starts a candidate on the peer, then here. When its port is
// taken on either end (another program, or a tunnel that already uses it),
// it moves to a random port free here and tries again, so a busy test port
// doesn't fail the test.
func (r *Runner) bringUp(ctx context.Context, c Candidate, ttl time.Duration) (up, error) {
	var err error
	for try := 0; try < portTries; try++ {
		if try > 0 {
			p, perr := freeTestPort()
			if perr != nil {
				return up{}, err
			}
			r.say("  port %d is taken; trying %d", c.Port, p)
			c = c.withPort(p)
		}
		var u up
		u, err = r.startBoth(ctx, c, ttl)
		if err == nil {
			return u, nil
		}
		if c.Port == 0 || !portTaken(err) || ctx.Err() != nil {
			return up{}, err
		}
	}
	return up{}, err
}

// startBoth brings c up on the peer and here, and undoes both on failure.
func (r *Runner) startBoth(ctx context.Context, c Candidate, ttl time.Duration) (up, error) {
	res, err := r.client.BenchUp(ctx, agent.BenchUpRequest{Self: r.Peer.Name, Tunnels: c.Tunnels, TTL: int(ttl.Seconds())})
	u := up{c: c}
	for _, s := range res.Specs {
		u.peer = append(u.peer, s.Name)
	}
	undo := func() {
		engine.RemoveBench(u.local)
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.client.BenchDown(cctx, u.peer)
	}
	if err != nil {
		undo()
		return up{}, fmt.Errorf("peer: %w", err)
	}
	isSelf := func(n string) bool { return n == r.State.Self }
	specs, errs := engine.RenderTunnels(isSelf, c.Tunnels, engine.RouteSource)
	if len(errs) > 0 {
		undo()
		return up{}, errs[0]
	}
	for i := range specs {
		specs[i].Name = agent.BenchIfname(specs[i].Tunnel)
		u.local = append(u.local, specs[i].Name)
	}
	if err := engine.ApplyBench(specs, ttl); err != nil {
		undo()
		return up{}, err
	}
	u.mtu = specs[len(specs)-1].MTU
	return u, nil
}

// portTaken reports whether bringing a tunnel up failed only because its
// port is in use on one of the two servers.
func portTaken(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") || strings.Contains(s, "already used") ||
		strings.Contains(s, "port is in use") || strings.Contains(s, "eaddrinuse")
}

// withPort moves a candidate's port (the bottom tunnel's, and any layer on
// the same port) to p.
func (c Candidate) withPort(p uint16) Candidate {
	old := c.Port
	ts := make([]config.Tunnel, len(c.Tunnels))
	copy(ts, c.Tunnels)
	for i := range ts {
		if ts[i].Port == old {
			ts[i].Port = p
		}
	}
	c.Tunnels, c.Port = ts, p
	c.Title = strings.Replace(c.Title, strconv.Itoa(int(old)), strconv.Itoa(int(p)), 1)
	return c
}

// freeTestPort picks a random port that is free here for both TCP and UDP.
func freeTestPort() (uint16, error) {
	for i := 0; i < 50; i++ {
		p := uint16(20000 + rand.IntN(40000))
		if portFree(p) {
			return p, nil
		}
	}
	return 0, errors.New("no free port for the test")
}

func portFree(p uint16) bool {
	addr := ":" + strconv.Itoa(int(p))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	pc.Close()
	return true
}
