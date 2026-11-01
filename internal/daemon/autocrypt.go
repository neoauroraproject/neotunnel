package daemon

// Automatic encryption: when a tunnel set to it stops passing traffic, try the
// next step of its ladder (no encryption, each IPsec cipher, a NeoObfs layer;
// see manage.CryptoRungs), restart it on both servers and give it some minutes
// before trying the one after.
//
// A tunnel whose forwards have another tunnel that works is left alone: not
// being cut off comes first, so they moved to it (see package failover) and
// this tunnel is only watched until it passes again and takes them back. Once
// every step has failed in a row, the ladder rests a while before it starts
// over, and a far server that can't be reached is not touched at all: both
// ends must change together.

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/events"
	"neotunnel/internal/failover"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/safe"
)

const (
	// autoAfter is how many bad rounds in a row lead to the next step. The
	// recovery's plain restart comes first, at the same count, and starts the
	// count over; so a tunnel a restart can fix keeps its encryption.
	autoAfter = restartAfter
	// autoWait is how long each step gets, unless the settings say otherwise.
	autoWait = 5 * time.Minute
	// autoRest is the pause after every step has failed in a row.
	autoRest = 30 * time.Minute
)

type autoCrypt struct {
	mu      sync.Mutex
	changed map[string]time.Time // when each tunnel last changed step
	start   map[string]string    // the step a tunnel's trouble began on
	running map[string]bool      // changes in progress
	failed  map[string]string    // why the last change of a tunnel failed
	change  func(ctx context.Context, id *identity.Identity, name, rung string) error
}

func newAutoCrypt() *autoCrypt {
	return &autoCrypt{changed: map[string]time.Time{}, start: map[string]string{}, running: map[string]bool{}, failed: map[string]string{}, change: manage.SetCryptoRung}
}

// waitOf is how long each step gets on this node.
func waitOf(st *config.State) time.Duration {
	if m := st.D().AutoWait; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return autoWait
}

// hasAlternative reports whether every forward that can run on tunnel has
// another of its tunnels that works, and there is at least one forward.
func hasAlternative(fs []config.Forward, tunnel string, hs map[string]health.State) bool {
	any := false
	for _, f := range fs {
		if !f.Uses(tunnel) {
			continue
		}
		if !slices.ContainsFunc(f.Paths(), func(n string) bool { return n != tunnel && failover.Working(hs[n]) }) {
			return false
		}
		any = true
	}
	return any
}

// next decides whether tunnel t takes another step now, and which.
func (a *autoCrypt) next(st *config.State, t config.Tunnel, fs []config.Forward, hs map[string]health.State, now time.Time) (string, bool) {
	h := hs[t.Name]
	if h.Good >= autoAfter {
		delete(a.start, t.Name) // it passes: the next trouble starts a new ladder
	}
	if h.Bad < autoAfter || a.running[t.Name] || hasAlternative(fs, t.Name, hs) {
		return "", false
	}
	cur := manage.CryptoRung(st, t.Name)
	rungs := manage.CryptoRungs(st, t.Name)
	at := slices.Index(rungs, cur)
	if at < 0 {
		return "", false
	}
	if _, ok := a.start[t.Name]; !ok {
		a.start[t.Name] = cur
	}
	to := rungs[(at+1)%len(rungs)]
	wait := waitOf(st)
	if to == a.start[t.Name] {
		wait = max(wait, autoRest) // every step failed in a row
	}
	if now.Sub(a.changed[t.Name]) < wait {
		return "", false
	}
	return to, true
}

// step moves the tunnels that need it to their next step, in the background
// so probing goes on.
func (a *autoCrypt) step(ctx context.Context, id *identity.Identity, st *config.State, tr *health.Tracker, reconcile func()) {
	if health.Quiet() || safe.Current() != nil {
		return
	}
	hs := tr.Snapshot()
	_, fs := st.Effective()
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range st.Tunnels {
		if !t.AutoEncrypt || t.Disabled || engine.Blocked(t.Name) {
			continue
		}
		to, ok := a.next(st, t, fs, hs, now)
		if !ok {
			continue
		}
		from := manage.CryptoRung(st, t.Name)
		a.running[t.Name] = true
		a.changed[t.Name] = now
		loss := hs[t.Name].Loss
		log.Printf("tunnel %s: lost %.0f%% of its probes; automatic encryption moves from %s to %s", t.Name, loss, rungName(from), rungName(to))
		go func(name string) {
			rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			err := a.change(rctx, id, name, to)
			reconcile()
			a.mu.Lock()
			delete(a.running, name)
			repeat := err != nil && a.failed[name] == err.Error()
			if err != nil {
				a.failed[name] = err.Error()
			} else {
				delete(a.failed, name)
			}
			a.mu.Unlock()
			if err != nil {
				log.Printf("tunnel %s: automatic encryption: %v", name, err)
				tr.Note(name, fmt.Sprintf("automatic encryption stays on %s: %v", rungName(from), err))
				if !repeat { // a far server that stays away is told about once
					events.Add(events.Event{Tunnel: name, Kind: events.Cipher, From: from, To: from, Auto: true, Loss: loss, Detail: err.Error()})
				}
				return
			}
			tr.Note(name, fmt.Sprintf("automatic encryption moved to %s", rungName(to)))
			events.Add(events.Event{Tunnel: name, Kind: events.Cipher, From: from, To: to, Auto: true, Loss: loss})
			if cur, err := config.Load(); err == nil {
				for _, n := range manage.Stack(cur, name) {
					tr.Fresh(n)
				}
			}
		}(t.Name)
	}
}

func rungName(r string) string {
	if r == "" {
		return "no encryption"
	}
	return r
}
