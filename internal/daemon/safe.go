package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/safe"
)

const safeEvery = 2 * time.Second

// safeHooks are how this node confirms a safe change with its peers and
// reverts one.
func safeHooks(id *identity.Identity) safe.Hooks {
	return safe.Hooks{
		ConfirmPeer: func(ctx context.Context, peer, change string) error {
			st, err := config.Load()
			if err != nil {
				return err
			}
			p, ok := st.Peer(peer)
			if !ok {
				return nil // removed meanwhile: nothing to confirm
			}
			c, err := agent.ToPeer(id, *p)
			if err != nil {
				return err
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err = c.SafeConfirm(cctx, change)
			if errors.Is(err, agent.ErrUnsupported) {
				// Too old for safe changes: it applied the change without a
				// hold, and answering at all shows the path still works.
				return nil
			}
			return err
		},
		Revert: func(prev safe.Snapshot, peers []string) error {
			_, ch, err := manage.Commit(peers, func(s *config.State) error {
				safe.Restore(s, prev)
				return nil
			})
			if err != nil {
				return err
			}
			for _, p := range ch.Peers {
				if !p.OK {
					return fmt.Errorf("%s: %s (it puts its own part back by itself)", p.Peer, p.Error)
				}
			}
			return nil
		},
	}
}

// watchSafe settles safe changes: on the server that made one it confirms
// with the peers once the panel kept it, or reverts it past its deadline; on
// a peer it puts back what the master had it run before when the master's
// confirmation didn't arrive in time.
func watchSafe(ctx context.Context, h safe.Hooks, reconcile func()) {
	t := time.NewTicker(safeEvery)
	defer t.Stop()
	for {
		if o := safe.Tick(ctx, time.Now(), h); o != nil {
			if o.Kept {
				log.Printf("safe change %q kept", o.What)
			} else {
				log.Printf("safe change %q reverted: %s", o.What, o.Reason)
			}
		}
		if hold := safe.ExpiredHold(time.Now()); hold != nil {
			st, err := config.Update(func(s *config.State) error {
				m := s.Managed // holds from before several masters name none
				if hold.Master != "" {
					m = s.MasterByKey(hold.Master)
				}
				if m == nil {
					return errors.New("no such master")
				}
				safe.RestoreManaged(m, hold)
				return nil
			})
			if err == nil {
				engine.Apply(st)
				reconcile()
				log.Printf("safe change %s was not confirmed by the master; previous configuration restored", hold.ID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
