// Package failover moves a forward to a backup tunnel when the one it runs on
// stops passing traffic, and back up the list once a tunnel before it has
// been healthy for a while. The daemon decides from the tunnels' health
// probes and keeps the active path of each forward in failover.json; every
// apply (daemon or CLI) reads that file, so the forward's rules follow the
// active tunnel.
package failover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
)

const (
	// FailAfter is how many bad probe rounds in a row (losing
	// health.BadLoss or more; a round every 8 s) the active tunnel must
	// have before its forwards move. It is short on purpose: with a backup
	// to go to, not being cut off comes first.
	FailAfter = 2
	// RecoverAfter is how many good rounds in a row the primary must have
	// before its forwards come back, so a tunnel that flaps doesn't drag
	// them along.
	RecoverAfter = 8
	// MinStay is the least time between two switches of a forward, unless
	// the tunnel it is on has failed.
	MinStay = time.Minute
	// Margin is how much lower (in loss points) a tunnel that is itself bad
	// must lose than the active one to be worth moving to.
	Margin = 20.0
	// MaxHistory is how many switches are kept per forward.
	MaxHistory = 20
	// BestAfter is how many rounds in a row (about five minutes) another
	// tunnel must be clearly better before a forward on the best path
	// moves to it, and BestStay the least time between two such moves.
	BestAfter = 38
	BestStay  = 30 * time.Minute
)

// Switch is one change of a forward's path.
type Switch struct {
	At     int64  `json:"at"` // unix seconds
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	// Better marks a move to a better path while the old one still works:
	// connections already open stay on it, only new ones move.
	Better bool `json:"better,omitempty"`
}

// Path is the active tunnel of one forward and how it got there.
type Path struct {
	Active   string   `json:"active"`
	Since    int64    `json:"since,omitempty"` // unix seconds of the last switch
	Switches []Switch `json:"switches,omitempty"`
	// Ahead counts, for a forward on the best path, the rounds in a row
	// each other tunnel was clearly better than the active one.
	Ahead map[string]int `json:"ahead,omitempty"`
}

// Paths is every forward's path, by Key.
type Paths map[string]*Path

// Key identifies a forward on this node: its primary tunnel and ports.
func Key(f config.Forward) string { return f.Tunnel + " " + f.Ports }

func path() string { return filepath.Join(config.Dir, "failover.json") }

// Load reads the saved paths (empty when there are none).
func Load() Paths {
	ps := Paths{}
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, &ps)
	}
	return ps
}

// Save writes the paths atomically.
func Save(ps Paths) error {
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	tmp := path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path())
}

// ActiveOf is the tunnel a forward runs on now.
func (ps Paths) ActiveOf(f config.Forward) string {
	if p := ps[Key(f)]; p != nil && slices.Contains(f.Backups(), p.Active) {
		return p.Active
	}
	return f.Tunnel
}

// Route returns the forwards with each one's Tunnel set to the tunnel it
// runs on now: its active path, or while that is turned off the first of
// its tunnels that isn't.
func Route(fs []config.Forward, ts []config.Tunnel, ps Paths) []config.Forward {
	off := map[string]bool{}
	for _, t := range ts {
		off[t.Name] = t.Disabled
	}
	out := make([]config.Forward, 0, len(fs))
	for _, f := range fs {
		if f.Backup != "" {
			active := ps.ActiveOf(f)
			if off[active] {
				for _, n := range f.Paths() {
					if !off[n] {
						active = n
						break
					}
				}
			}
			f.Tunnel = active
		}
		out = append(out, f)
	}
	return out
}

// Decide returns the tunnel a forward should run on, given the one it runs
// on now and the tunnels' health, with the reason when it changes.
//
//   - A tunnel earlier in the forward's list that had RecoverAfter good rounds
//     in a row takes the forward back.
//   - Once the active tunnel had FailAfter bad rounds in a row (most of its
//     probes lost, not just all of them), the forward moves to the first
//     tunnel of its list that works, healthy or merely degraded; failing
//     that, to the first that, bad itself, loses at least Margin points less.
//
// A tunnel never probed is never better than anything.
func Decide(f config.Forward, active string, hs map[string]health.State) (string, string) {
	list := f.Paths()
	rank := slices.Index(list, active)
	if len(list) == 1 || rank < 0 {
		return f.Tunnel, ""
	}
	for _, n := range list[:rank] {
		if hs[n].Good >= RecoverAfter {
			return n, fmt.Sprintf("%s passed %d rounds in a row", n, hs[n].Good)
		}
	}
	cur := hs[active]
	if !failing(cur) {
		return active, ""
	}
	moved := func(n string) (string, string) {
		return n, fmt.Sprintf("%s lost %.0f%% of its probes for %d rounds; %s loses %.0f%%", active, cur.Loss, cur.Bad, n, hs[n].Loss)
	}
	for _, n := range list {
		if n != active && Working(hs[n]) {
			return moved(n)
		}
	}
	for _, n := range list {
		if n != active && better(hs[n], cur) {
			return moved(n)
		}
	}
	return active, ""
}

// failing: the tunnel has lost most of its probes for FailAfter rounds.
func failing(s health.State) bool { return s.Bad >= FailAfter }

// Working reports whether a tunnel carries traffic: it was probed, and at
// worst it is degraded.
func Working(s health.State) bool {
	return s.Probes > 0 && s.Passes && (s.Grade == health.GradeOK || s.Grade == health.GradeDegraded)
}

// better reports whether tunnel a is worth moving to from b.
func better(a, b health.State) bool {
	if a.Probes == 0 || !a.Passes {
		return false
	}
	return Working(a) || a.Loss+Margin <= b.Loss
}

// Speeds are the tunnels' download speeds in the last night's test
// (Mbit/s), to tell paths that ping and lose alike apart; nil = none.
var (
	speedsMu sync.Mutex
	speeds   map[string]float64
)

// SetSpeeds records the night's speed test results.
func SetSpeeds(m map[string]float64) {
	speedsMu.Lock()
	speeds = m
	speedsMu.Unlock()
}

func speedOf(name string) float64 {
	speedsMu.Lock()
	defer speedsMu.Unlock()
	return speeds[name]
}

// beats reports whether tunnel c is clearly better than a, both working: it
// loses clearly less without answering much slower; or loses about as
// little and answers clearly faster; or does as well in both and carried
// clearly more in the night's speed test.
func beats(c, a health.State, sc, sa float64) bool {
	if !Working(c) || !Working(a) || c.AvgMs <= 0 || a.AvgMs <= 0 {
		return false
	}
	switch {
	case c.Loss+2 <= a.Loss && c.AvgMs <= a.AvgMs*1.3:
		return true
	case c.Loss > a.Loss+1:
		return false
	case c.AvgMs <= a.AvgMs*0.85 && a.AvgMs-c.AvgMs >= 10:
		return true
	case c.AvgMs <= a.AvgMs*1.1 && sc > 0 && sa > 0 && sc >= sa*1.5:
		return true
	}
	return false
}

// best decides for a forward on the best path: away at once from a tunnel
// that fails, to the best of those that work; otherwise to one that was
// clearly better for BestAfter rounds, at most once in BestStay.
func best(f config.Forward, p *Path, hs map[string]health.State, now time.Time) (string, string, bool) {
	list := f.Paths()
	cur := hs[p.Active]
	if !slices.Contains(list, p.Active) {
		return f.Tunnel, "", false
	}
	if failing(cur) {
		pick := ""
		for _, n := range list {
			if n != p.Active && Working(hs[n]) && (pick == "" || beats(hs[n], hs[pick], speedOf(n), speedOf(pick))) {
				pick = n
			}
		}
		if pick == "" {
			for _, n := range list {
				if n != p.Active && better(hs[n], cur) {
					pick = n
					break
				}
			}
		}
		if pick != "" {
			return pick, fmt.Sprintf("%s lost %.0f%% of its probes for %d rounds; %s loses %.0f%%", p.Active, cur.Loss, cur.Bad, pick, hs[pick].Loss), false
		}
		return p.Active, "", false
	}
	if p.Ahead == nil {
		p.Ahead = map[string]int{}
	}
	pick := ""
	for _, n := range list {
		if n == p.Active || !beats(hs[n], cur, speedOf(n), speedOf(p.Active)) {
			delete(p.Ahead, n)
			continue
		}
		p.Ahead[n]++
		if p.Ahead[n] >= BestAfter && (pick == "" || beats(hs[n], hs[pick], speedOf(n), speedOf(pick))) {
			pick = n
		}
	}
	if pick == "" || p.Since != 0 && now.Sub(time.Unix(p.Since, 0)) < BestStay {
		return p.Active, "", false
	}
	c := hs[pick]
	return pick, fmt.Sprintf("%s is better: %.0f%% loss and %.0f ms against %.0f%% and %.0f ms on %s, for 5 minutes", pick, c.Loss, c.AvgMs, cur.Loss, cur.AvgMs, p.Active), true
}

// Step runs Decide for every forward with a backup (best for those on the
// best path), records the switches in ps and drops the paths of forwards
// that are gone. It returns the switches made, keyed like ps.
func Step(ps Paths, fs []config.Forward, hs map[string]health.State, now time.Time) map[string]Switch {
	made := map[string]Switch{}
	keep := map[string]bool{}
	for _, f := range fs {
		if f.Backup == "" {
			continue
		}
		k := Key(f)
		keep[k] = true
		p := ps[k]
		if p == nil {
			p = &Path{Active: f.Tunnel}
			ps[k] = p
		}
		var next, why string
		var gentle bool
		if f.Best {
			next, why, gentle = best(f, p, hs, now)
		} else {
			p.Ahead = nil
			next, why = Decide(f, p.Active, hs)
		}
		if next == p.Active || (p.Since != 0 && now.Sub(time.Unix(p.Since, 0)) < MinStay && why != "" && !failing(hs[p.Active])) {
			continue
		}
		if why == "" {
			why = "the forward's tunnels changed"
		}
		s := Switch{At: now.Unix(), From: p.Active, To: next, Reason: why, Better: gentle}
		p.Ahead = nil
		p.Active, p.Since = next, s.At
		p.Switches = append(p.Switches, s)
		if len(p.Switches) > MaxHistory {
			p.Switches = p.Switches[len(p.Switches)-MaxHistory:]
		}
		made[k] = s
	}
	for k := range ps {
		if !keep[k] {
			delete(ps, k)
		}
	}
	return made
}
