package daemon

// Automatic recovery of tunnels that are up but don't carry traffic.
//
// An interface can exist, and a ping now and then can come back, while the
// tunnel loses most of its packets: a path that throttles this one flow, a
// stale kernel state after the far server restarted, a NAT or firewall that
// dropped the flow. Rebuilding the tunnel on both ends (new interfaces, new
// keys, new connections and, for UDP carriers, new source ports) often
// clears it. So a tunnel this node manages that has lost most of its probes
// for restartAfter rounds in a row is restarted once on both servers. Its
// count then starts over: if it is still bad a few rounds later, the
// failover step moves its forwards to a backup that does better (see
// package failover). A tunnel is restarted at most once per restartGap, and
// nothing happens while a speed test fills the tunnels on purpose.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
)

const (
	// restartAfter is how many bad rounds in a row (losing health.BadLoss
	// or more of a round's probes, a round every 8 s) lead to a restart.
	restartAfter = 3
	// restartGap is the least time between two automatic restarts of a
	// tunnel, so one that stays broken isn't restarted all the time.
	restartGap = 10 * time.Minute
)

type recovery struct {
	mu      sync.Mutex
	last    map[string]time.Time // last automatic restart, by tunnel
	running map[string]bool      // restarts in progress
	restart func(ctx context.Context, id *identity.Identity, name string) error
}

func newRecovery() *recovery {
	return &recovery{last: map[string]time.Time{}, running: map[string]bool{}, restart: manage.RestartTunnel}
}

// step restarts the tunnels that need it. A restart runs in the background,
// so probing goes on; the tunnel's rounds start over at once, so failover
// waits for rounds after the restart before it gives up on the tunnel.
func (r *recovery) step(ctx context.Context, id *identity.Identity, st *config.State, tr *health.Tracker) {
	if st.D().NoRecover || health.Quiet() {
		return
	}
	hs := tr.Snapshot()
	ts, _ := st.Effective()
	carrier := map[string]bool{}
	for _, t := range ts {
		if t.Over != "" {
			carrier[t.Over] = true
		}
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range ts {
		h := hs[t.Name]
		// A tunnel another server manages is restarted by that server; a
		// carrier is restarted with the tunnel on top of it (the restart
		// takes the whole stack down and up).
		if t.MasterKey != "" || t.Disabled || carrier[t.Name] || h.Bad < restartAfter {
			continue
		}
		if r.running[t.Name] || now.Sub(r.last[t.Name]) < restartGap {
			continue
		}
		stack := manage.Stack(st, t.Name)
		why := fmt.Sprintf("lost %.0f%% of its probes for %d rounds", h.Loss, h.Bad)
		r.running[t.Name] = true
		for _, n := range stack {
			r.last[n] = now
			tr.Fresh(n)
		}
		log.Printf("tunnel %s: %s; restarting it on both servers", t.Name, why)
		go func(name string) {
			rctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			err := r.restart(rctx, id, name)
			note := "restarted automatically: " + why
			if err != nil {
				note += fmt.Sprintf(" (far server: %v)", err)
				log.Printf("tunnel %s: restart on the far server: %v", name, err)
			}
			tr.Note(name, note)
			r.mu.Lock()
			delete(r.running, name)
			r.mu.Unlock()
		}(t.Name)
	}
}
