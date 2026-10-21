// Package failover moves a forward to its backup tunnel when the primary
// stops passing traffic, and back once the primary has been healthy for a
// while. The daemon decides from the tunnels' health probes and keeps the
// active path of each forward in failover.json; every apply (daemon or CLI)
// reads that file, so the forward's rules follow the active tunnel.
package failover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
)

const (
	// FailAfter is how many bad probe rounds in a row (losing
	// health.BadLoss or more; a round every 8 s) the active tunnel must
	// have before its forwards move.
	FailAfter = 3
	// RecoverAfter is how many good rounds in a row the primary must have
	// before its forwards come back, so a tunnel that flaps doesn't drag
	// them along.
	RecoverAfter = 8
	// MinStay is the least time between two switches of a forward.
	MinStay = time.Minute
	// Margin is how much lower (in loss points) a tunnel that is itself bad
	// must lose than the active one to be worth moving to.
	Margin = 20.0
	// MaxHistory is how many switches are kept per forward.
	MaxHistory = 20
)

// Switch is one change of a forward's path.
type Switch struct {
	At     int64  `json:"at"` // unix seconds
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Path is the active tunnel of one forward and how it got there.
type Path struct {
	Active   string   `json:"active"`
	Since    int64    `json:"since,omitempty"` // unix seconds of the last switch
	Switches []Switch `json:"switches,omitempty"`
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
	if f.Backup == "" {
		return f.Tunnel
	}
	if p := ps[Key(f)]; p != nil && p.Active == f.Backup {
		return f.Backup
	}
	return f.Tunnel
}

// Route returns the forwards with each one's Tunnel set to the tunnel it
// runs on now: its backup while the primary is failed over, or while the
// primary is turned off and the backup isn't.
func Route(fs []config.Forward, ts []config.Tunnel, ps Paths) []config.Forward {
	off := map[string]bool{}
	for _, t := range ts {
		off[t.Name] = t.Disabled
	}
	out := make([]config.Forward, 0, len(fs))
	for _, f := range fs {
		if f.Backup != "" {
			if ps.ActiveOf(f) == f.Backup || (off[f.Tunnel] && !off[f.Backup]) {
				f.Tunnel = f.Backup
			}
		}
		out = append(out, f)
	}
	return out
}

// Decide returns the tunnel a forward should run on, given the one it runs
// on now and the tunnels' health, with the reason when it changes.
//
//   - On the primary: move to the backup once the primary had FailAfter bad
//     rounds in a row (most of its probes lost, not just all of them) and
//     the backup is better: healthy or merely degraded, or, bad itself,
//     losing at least Margin points less.
//   - On the backup: come back once the primary had RecoverAfter good
//     rounds in a row, or when the backup went bad and the primary is
//     better than it.
//
// A tunnel never probed is never better than anything.
func Decide(f config.Forward, active string, hs map[string]health.State) (string, string) {
	if f.Backup == "" || (active != f.Tunnel && active != f.Backup) {
		return f.Tunnel, ""
	}
	p, b := hs[f.Tunnel], hs[f.Backup]
	if active == f.Tunnel {
		if failing(p) && better(b, p) {
			return f.Backup, fmt.Sprintf("%s lost %.0f%% of its probes for %d rounds; %s loses %.0f%%", f.Tunnel, p.Loss, p.Bad, f.Backup, b.Loss)
		}
		return active, ""
	}
	switch {
	case p.Good >= RecoverAfter:
		return f.Tunnel, fmt.Sprintf("%s passed %d rounds in a row", f.Tunnel, p.Good)
	case failing(b) && better(p, b):
		return f.Tunnel, fmt.Sprintf("%s lost %.0f%% of its probes for %d rounds; %s loses %.0f%%", f.Backup, b.Loss, b.Bad, f.Tunnel, p.Loss)
	}
	return active, ""
}

// failing: the tunnel has lost most of its probes for FailAfter rounds.
func failing(s health.State) bool { return s.Bad >= FailAfter }

// better reports whether tunnel a is worth moving to from b.
func better(a, b health.State) bool {
	if a.Probes == 0 || !a.Passes {
		return false
	}
	if a.Grade == health.GradeOK || a.Grade == health.GradeDegraded {
		return true
	}
	return a.Loss+Margin <= b.Loss
}

// Step runs Decide for every forward with a backup, records the switches in
// ps and drops the paths of forwards that are gone. It returns the switches
// made, keyed like ps.
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
		next, why := Decide(f, p.Active, hs)
		if next == p.Active || (p.Since != 0 && now.Sub(time.Unix(p.Since, 0)) < MinStay && why != "") {
			continue
		}
		if why == "" {
			why = "the forward's tunnels changed"
		}
		s := Switch{At: now.Unix(), From: p.Active, To: next, Reason: why}
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
