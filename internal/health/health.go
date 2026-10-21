// Package health tracks whether each tunnel actually carries traffic: every
// few seconds the daemon sends a short round of pings to the far inner
// address and keeps about a minute of results, so the panel can say how
// well packets pass, not just that the interface exists, and the daemon can
// act on a tunnel that is up but loses most of its packets.
package health

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
)

const (
	// Window is how many probe packets the rolling loss and RTT cover:
	// about a minute, at a round of 5 every 8 seconds.
	Window = 40
	// A round that loses BadLoss percent or more is bad; one that loses at
	// most GoodLoss is good. Between them it is neither, and both streaks
	// start over.
	BadLoss  = 40.0
	GoodLoss = 10.0
	// DegradedLoss is the rolling loss from which a tunnel is degraded.
	DegradedLoss = 5.0
)

// Grades, from the rolling window and the latest rounds.
const (
	GradeOK       = "ok"       // loss under DegradedLoss
	GradeDegraded = "degraded" // packets pass, with noticeable loss
	GradeBad      = "bad"      // most packets lost, now or over the window
	GradeDown     = "down"     // nothing came back in the whole window
)

// State is one tunnel's live health.
type State struct {
	RTTms float64 `json:"rtt_ms"` // last round's average RTT, 0 if it lost everything
	AvgMs float64 `json:"avg_ms"` // average over the window
	Loss  float64 `json:"loss"`   // percent lost over the window
	// LastLoss is the percent lost in the latest round.
	LastLoss float64 `json:"last_loss"`
	Last     int64   `json:"last"`   // unix of the last reply, 0 = never
	Probes   int     `json:"probes"` // probe packets in the window so far
	Passes   bool    `json:"passes"` // at least one reply in the window
	Grade    string  `json:"grade"`
	// Bad and Good count the rounds in a row that were bad or good (one of
	// them is always 0), for decisions that must not flap.
	Bad  int `json:"bad_rounds,omitempty"`
	Good int `json:"good_rounds,omitempty"`
	// Action is the daemon's last automatic step for this tunnel and why
	// ("restarted: lost 95% for 24 s"), at ActionAt (unix).
	Action   string `json:"action,omitempty"`
	ActionAt int64  `json:"action_at,omitempty"`

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

// Record adds a single probe (a round of one).
func (t *Tracker) Record(tunnel string, ok bool, rtt time.Duration) {
	got := 0
	if ok {
		got = 1
	}
	t.RecordRound(tunnel, 1, got, rtt)
}

// RecordRound adds a round of sent probes, got of which came back with an
// average round-trip time of rtt.
func (t *Tracker) RecordRound(tunnel string, sent, got int, rtt time.Duration) {
	if sent <= 0 {
		return
	}
	got = max(0, min(got, sent))
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.state(tunnel)
	for i := range sent {
		s.recent = append(s.recent, i < got)
	}
	if len(s.recent) > Window {
		s.recent = s.recent[len(s.recent)-Window:]
	}
	s.LastLoss = 100 * float64(sent-got) / float64(sent)
	if got > 0 {
		ms := float64(rtt.Microseconds()) / 1000
		s.RTTms, s.Last = ms, time.Now().Unix()
		s.rtts = append(s.rtts, ms)
		if len(s.rtts) > Window {
			s.rtts = s.rtts[1:]
		}
	} else {
		s.RTTms = 0
	}
	switch {
	case s.LastLoss >= BadLoss:
		s.Bad, s.Good = s.Bad+1, 0
	case s.LastLoss <= GoodLoss:
		s.Bad, s.Good = 0, s.Good+1
	default:
		s.Bad, s.Good = 0, 0
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
	s.Grade = grade(s)
}

func grade(s *State) string {
	switch {
	case !s.Passes:
		return GradeDown
	case s.Loss >= BadLoss || s.Bad >= 2:
		return GradeBad
	case s.Loss >= DegradedLoss || s.Bad > 0:
		return GradeDegraded
	}
	return GradeOK
}

func (t *Tracker) state(tunnel string) *State {
	s := t.m[tunnel]
	if s == nil {
		s = &State{}
		t.m[tunnel] = s
	}
	return s
}

// Note records an automatic action on a tunnel, for the panel and logs.
func (t *Tracker) Note(tunnel, action string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.state(tunnel)
	s.Action, s.ActionAt = action, time.Now().Unix()
}

// Fresh starts a tunnel's streaks over (after it was restarted), so it is
// judged again on rounds that follow the restart only.
func (t *Tracker) Fresh(tunnel string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.m[tunnel]; s != nil {
		s.Bad, s.Good = 0, 0
	}
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

// quietFile marks a speed test in progress (until a unix time), from any
// process: a test fills the tunnels on purpose, and the loss it causes must
// not be taken for a broken tunnel.
var quietFile = "/run/neotunnel/quiet"

// QuietFor holds automatic actions off for d from now.
func QuietFor(d time.Duration) {
	until := time.Now().Add(d).Unix()
	if cur := quietUntil(); cur > until {
		return
	}
	os.MkdirAll(filepath.Dir(quietFile), 0o755)
	os.WriteFile(quietFile, []byte(strconv.FormatInt(until, 10)), 0o644)
}

// Quiet reports whether a speed test asked for automatic actions to wait.
func Quiet() bool { return time.Now().Unix() < quietUntil() }

func quietUntil() int64 {
	b, err := os.ReadFile(quietFile)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}
