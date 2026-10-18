// Package health tracks whether each tunnel actually carries traffic: the
// daemon pings the far inner address every few seconds and keeps a short
// history of round-trip time and loss, so the panel can show "up" only when
// packets really pass, not just when the interface exists.
package health

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"neotunnel/internal/config"
)

// Window is how many probes the rolling loss/RTT is computed over.
const Window = 20

// State is one tunnel's live health.
type State struct {
	RTTms  float64 `json:"rtt_ms"` // last round-trip time, 0 if the last probe was lost
	AvgMs  float64 `json:"avg_ms"` // average over the window
	Loss   float64 `json:"loss"`   // percent lost over the window
	Last   int64   `json:"last"`   // unix of the last reply, 0 = never
	Probes int     `json:"probes"` // probes in the window so far
	Passes bool    `json:"passes"` // at least one reply in the window
	// Up and Down count the probes in a row that got a reply or were lost
	// (one of them is always 0), for decisions that must not flap.
	Up     int    `json:"up_streak,omitempty"`
	Down   int    `json:"down_streak,omitempty"`
	recent []bool // got a reply, newest last
	rtts   []float64
}

// Tracker holds every tunnel's health, safe for concurrent use.
type Tracker struct {
	mu sync.Mutex
	m  map[string]*State
}

func New() *Tracker { return &Tracker{m: map[string]*State{}} }

func statePath() string { return filepath.Join(config.Dir, "health.json") }

// Record adds one probe result (rtt 0 and ok=false when lost).
func (t *Tracker) Record(tunnel string, ok bool, rtt time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.m[tunnel]
	if s == nil {
		s = &State{}
		t.m[tunnel] = s
	}
	s.recent = append(s.recent, ok)
	if len(s.recent) > Window {
		s.recent = s.recent[1:]
	}
	ms := float64(rtt.Microseconds()) / 1000
	if ok {
		s.RTTms, s.Last = ms, time.Now().Unix()
		s.Up, s.Down = s.Up+1, 0
		s.rtts = append(s.rtts, ms)
		if len(s.rtts) > Window {
			s.rtts = s.rtts[1:]
		}
	} else {
		s.RTTms = 0
		s.Up, s.Down = 0, s.Down+1
	}
	lost, sum := 0, 0.0
	for _, g := range s.recent {
		if !g {
			lost++
		}
	}
	for _, r := range s.rtts {
		sum += r
	}
	s.Probes = len(s.recent)
	s.Loss = 100 * float64(lost) / float64(len(s.recent))
	if len(s.rtts) > 0 {
		s.AvgMs = sum / float64(len(s.rtts))
	}
	s.Passes = lost < len(s.recent)
}

// Forget drops tunnels no longer present.
func (t *Tracker) Forget(keep map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name := range t.m {
		if !keep[name] {
			delete(t.m, name)
		}
	}
}

// Snapshot returns a copy of every tunnel's state.
func (t *Tracker) Snapshot() map[string]State {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]State, len(t.m))
	for k, v := range t.m {
		out[k] = *v
	}
	return out
}

// Save persists the snapshot so the panel has data right after a restart.
func (t *Tracker) Save() {
	b, err := json.Marshal(t.Snapshot())
	if err == nil {
		os.WriteFile(statePath(), b, 0o600)
	}
}

// Load reads the last snapshot (best effort).
func Load() map[string]State {
	m := map[string]State{}
	if b, err := os.ReadFile(statePath()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// Pinger reports whether a round-trip to addr succeeded and how long it took.
type Pinger func(ctx context.Context, addr string) (time.Duration, bool)
