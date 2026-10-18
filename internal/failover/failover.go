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
	// FailAfter is how many probes in a row the primary must lose before
	// its forwards move to the backup (the daemon probes every 8 s).
	FailAfter = 3
	// RecoverAfter is how many probes in a row the primary must pass before
	// its forwards come back, so a tunnel that flaps doesn't drag them along.
	RecoverAfter = 6
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
//   - On the primary: move to the backup once the primary lost FailAfter
//     probes in a row and the backup's last probe got through.
//   - On the backup: come back once the primary passed RecoverAfter probes
//     in a row, or at once when the backup failed too and the primary's last
//     probe got through.
func Decide(f config.Forward, active string, hs map[string]health.State) (string, string) {
	if f.Backup == "" || (active != f.Tunnel && active != f.Backup) {
		return f.Tunnel, ""
	}
	p, b := hs[f.Tunnel], hs[f.Backup]
	if active == f.Tunnel {
		if p.Down >= FailAfter && b.Up > 0 {
			return f.Backup, fmt.Sprintf("%s lost %d probes in a row", f.Tunnel, p.Down)
		}
		return active, ""
	}
	switch {
	case p.Up >= RecoverAfter:
		return f.Tunnel, fmt.Sprintf("%s passed %d probes in a row", f.Tunnel, p.Up)
	case b.Down >= FailAfter && p.Up > 0:
		return f.Tunnel, fmt.Sprintf("%s lost %d probes in a row and %s answers", f.Backup, b.Down, f.Tunnel)
	}
	return active, ""
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
		if next == p.Active {
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
