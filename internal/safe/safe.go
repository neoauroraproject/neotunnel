// Package safe applies risky changes the way MikroTik's safe mode does: the
// tunnels and forwards from before the change are kept in the config dir,
// and unless the panel and every peer the change touched confirm within
// Wait, the daemon puts them back on both servers.
//
// On the server that made the change a Pending holds the snapshot; the
// panel confirms with Keep, then the daemon confirms with each peer (Tick).
// Each peer keeps a Hold of its own: pushes made while a change is pending
// carry its id, and a peer that hears no confirmation by its deadline
// reverts by itself, so a change that cut the path between the servers is
// still undone on both ends.
package safe

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
)

const (
	// Wait is how long a change has to be confirmed.
	Wait = 60 * time.Second
	// PeerGrace is how much longer a peer waits than the server that made
	// the change, so that server's own revert reaches it first when it can.
	PeerGrace = 15 * time.Second
)

// Snapshot is the part of the state a safe change can put back.
type Snapshot struct {
	Tunnels  []config.Tunnel  `json:"tunnels,omitempty"`
	Forwards []config.Forward `json:"forwards,omitempty"`
}

// Pending is a change waiting for confirmation on the server that made it.
type Pending struct {
	ID       string    `json:"id"`
	What     string    `json:"what"` // what changed, for the panel and the log
	Started  time.Time `json:"started"`
	Deadline time.Time `json:"deadline"`
	// Peers are the servers the change was pushed to; each must confirm.
	Peers []string `json:"peers,omitempty"`
	// Kept is set once the panel confirmed; Acked lists the peers that did.
	Kept  bool     `json:"kept,omitempty"`
	Acked []string `json:"acked,omitempty"`
	Prev  Snapshot `json:"prev"`
}

// Outcome is how the last safe change ended.
type Outcome struct {
	ID     string    `json:"id"`
	What   string    `json:"what"`
	Kept   bool      `json:"kept"`
	Reason string    `json:"reason,omitempty"` // why it was reverted
	At     time.Time `json:"at"`
}

// Hooks are the actions Tick and RevertNow take on the network.
type Hooks struct {
	// ConfirmPeer tells a peer the change is kept (it drops its hold).
	ConfirmPeer func(ctx context.Context, peer, id string) error
	// Revert saves prev's tunnels and forwards as the current ones,
	// applies them here and pushes them to peers.
	Revert func(prev Snapshot, peers []string) error
}

// ErrBusy is returned while another safe change waits for confirmation.
var ErrBusy = errors.New("a safe change is waiting for confirmation; keep or undo it first")

var mu sync.Mutex

func pendingPath() string { return filepath.Join(config.Dir, "safe.json") }
func outcomePath() string { return filepath.Join(config.Dir, "safe-last.json") }

// holdPath is where a peer keeps the hold of one master's pending change:
// the first master's in safe-hold.json, as before; another's next to it.
func holdPath(master string) string {
	if master == "" {
		return filepath.Join(config.Dir, "safe-hold.json")
	}
	h := sha256.Sum256([]byte(master))
	return filepath.Join(config.Dir, "safe-hold-"+hex.EncodeToString(h[:6])+".json")
}

// holdPaths lists the hold files there are.
func holdPaths() []string {
	ps, _ := filepath.Glob(filepath.Join(config.Dir, "safe-hold*.json"))
	return ps
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func snapshot(st *config.State) Snapshot {
	return Snapshot{
		Tunnels:  append([]config.Tunnel(nil), st.Tunnels...),
		Forwards: append([]config.Forward(nil), st.Forwards...),
	}
}

func load() *Pending {
	var p Pending
	if !readJSON(pendingPath(), &p) || p.ID == "" {
		return nil
	}
	return &p
}

// Current returns the change waiting for confirmation, if any.
func Current() *Pending {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

// Last returns how the last safe change ended, if one did.
func Last() *Outcome {
	var o Outcome
	if !readJSON(outcomePath(), &o) || o.ID == "" {
		return nil
	}
	return &o
}

// Begin snapshots st's tunnels and forwards before a change described by
// what. Call SetPeers once the change is pushed, or Cancel if it failed.
func Begin(st *config.State, what string, now time.Time) (Pending, error) {
	mu.Lock()
	defer mu.Unlock()
	if load() != nil {
		return Pending{}, ErrBusy
	}
	p := Pending{ID: newID(), What: what, Started: now, Deadline: now.Add(Wait), Prev: snapshot(st)}
	return p, writeJSON(pendingPath(), p)
}

// SetPeers records the servers a pending change was pushed to.
func SetPeers(id string, peers []string) error {
	return update(id, func(p *Pending) { p.Peers = peers })
}

// Cancel drops a pending change without reverting it (the change itself
// failed, so there is nothing to undo).
func Cancel(id string) {
	mu.Lock()
	defer mu.Unlock()
	if p := load(); p != nil && p.ID == id {
		os.Remove(pendingPath())
	}
}

// Keep is the panel's confirmation: the change still lets it in. The
// change is kept once the peers confirm too (Tick).
func Keep(id string) error {
	return update(id, func(p *Pending) { p.Kept = true })
}

func update(id string, fn func(*Pending)) error {
	mu.Lock()
	defer mu.Unlock()
	p := load()
	if p == nil || p.ID != id {
		return errors.New("no such change is waiting; it may have been reverted already")
	}
	fn(p)
	return writeJSON(pendingPath(), p)
}

// HoldFor is what a push to a peer carries while a change is pending: the
// change's id and how long the peer waits before it reverts by itself.
func HoldFor(now time.Time) (string, int) {
	p := Current()
	if p == nil || !now.Before(p.Deadline) {
		return "", 0
	}
	return p.ID, int((p.Deadline.Sub(now) + PeerGrace + time.Second - 1) / time.Second)
}

// Tick moves a pending change along: once the panel kept it, it asks the
// peers to confirm, and past the deadline it reverts. It returns the
// outcome when the change ended.
func Tick(ctx context.Context, now time.Time, h Hooks) *Outcome {
	p := Current()
	if p == nil {
		return nil
	}
	if p.Kept {
		for _, peer := range p.Peers {
			if slices.Contains(p.Acked, peer) {
				continue
			}
			if err := h.ConfirmPeer(ctx, peer, p.ID); err == nil {
				update(p.ID, func(q *Pending) { q.Acked = append(q.Acked, peer) })
				p.Acked = append(p.Acked, peer)
			}
		}
		if len(p.Acked) >= len(p.Peers) {
			return finish(p, Outcome{Kept: true}, nil, h)
		}
	}
	if now.Before(p.Deadline) {
		return nil
	}
	reason := "the panel did not confirm in time"
	if p.Kept {
		var missing []string
		for _, peer := range p.Peers {
			if !slices.Contains(p.Acked, peer) {
				missing = append(missing, peer)
			}
		}
		reason = strings.Join(missing, ", ") + " did not confirm in time"
	}
	return finish(p, Outcome{Reason: reason}, &p.Prev, h)
}

// RevertNow undoes a pending change at once (the panel's undo button).
func RevertNow(id string, h Hooks) (*Outcome, error) {
	p := Current()
	if p == nil || p.ID != id {
		return nil, errors.New("no such change is waiting; it may have been reverted already")
	}
	return finish(p, Outcome{Reason: "undone from the panel"}, &p.Prev, h), nil
}

// finish ends a pending change, reverting to prev when it's set. The pending
// file goes first, so the revert's own pushes carry no hold.
func finish(p *Pending, o Outcome, prev *Snapshot, h Hooks) *Outcome {
	mu.Lock()
	cur := load()
	if cur == nil || cur.ID != p.ID {
		mu.Unlock()
		return nil // finished by someone else meanwhile
	}
	os.Remove(pendingPath())
	mu.Unlock()
	o.ID, o.What, o.At = p.ID, p.What, time.Now()
	// Recorded before reverting: pushing to a peer the change cut off takes
	// a while, and the panel should say what is happening meanwhile.
	writeJSON(outcomePath(), o)
	if prev != nil {
		if err := h.Revert(*prev, p.Peers); err != nil {
			o.Reason += fmt.Sprintf("; revert: %v", err)
			writeJSON(outcomePath(), o)
		}
	}
	return &o
}

// Restore puts snap's tunnels and forwards into st (the revert's change).
func Restore(st *config.State, snap Snapshot) {
	st.Tunnels = append([]config.Tunnel(nil), snap.Tunnels...)
	st.Forwards = append([]config.Forward(nil), snap.Forwards...)
}

// Hold is a peer's side of a pending change: what its master had it run
// before, kept until the master confirms or the deadline passes.
type Hold struct {
	ID       string           `json:"id"`
	Master   string           `json:"master,omitempty"` // the master's key ("" = the first)
	Deadline time.Time        `json:"deadline"`
	Revision uint64           `json:"revision"`
	Tunnels  []config.Tunnel  `json:"tunnels,omitempty"`
	Forwards []config.Forward `json:"forwards,omitempty"`
}

func loadHold(path string) *Hold {
	var h Hold
	if !readJSON(path, &h) || h.ID == "" {
		return nil
	}
	return &h
}

// masterPath is the hold file of master m: safe-hold.json for the first.
func masterPath(st *config.State, m *config.Managed) string {
	if st.Managed == m {
		return holdPath("")
	}
	return holdPath(m.Master.PublicKey)
}

// HoldManaged is called on a peer for a push that carries a hold, before
// m is replaced: the first push of a change keeps m as it was; later
// pushes of the same change keep that first snapshot. A push without a
// hold (id "") drops any hold: it is the master's settled configuration.
func HoldManaged(id string, seconds int, st *config.State, m *config.Managed, now time.Time) error {
	mu.Lock()
	defer mu.Unlock()
	path := masterPath(st, m)
	if id == "" {
		os.Remove(path)
		return nil
	}
	if seconds <= 0 || seconds > 600 {
		seconds = int((Wait + PeerGrace) / time.Second)
	}
	h := loadHold(path)
	if h == nil || h.ID != id {
		h = &Hold{ID: id, Master: m.Master.PublicKey, Revision: m.Revision,
			Tunnels:  append([]config.Tunnel(nil), m.Tunnels...),
			Forwards: append([]config.Forward(nil), m.Forwards...)}
	}
	h.Deadline = now.Add(time.Duration(seconds) * time.Second)
	return writeJSON(path, h)
}

// ConfirmHold drops the hold of change id and reports whether it was held.
func ConfirmHold(id string) bool {
	mu.Lock()
	defer mu.Unlock()
	for _, path := range holdPaths() {
		if h := loadHold(path); h != nil && h.ID == id {
			os.Remove(path)
			return true
		}
	}
	return false
}

// ExpiredHold returns the hold whose deadline passed (and drops it), for
// the daemon to put the peer's configuration back.
func ExpiredHold(now time.Time) *Hold {
	mu.Lock()
	defer mu.Unlock()
	for _, path := range holdPaths() {
		if h := loadHold(path); h != nil && !now.Before(h.Deadline) {
			os.Remove(path)
			return h
		}
	}
	return nil
}

// RestoreManaged puts a hold's snapshot back into the managed state.
func RestoreManaged(m *config.Managed, h *Hold) {
	m.Revision = h.Revision
	m.Tunnels = append([]config.Tunnel(nil), h.Tunnels...)
	m.Forwards = append([]config.Forward(nil), h.Forwards...)
}
