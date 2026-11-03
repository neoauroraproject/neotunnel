// Package speedlog keeps the results of the nightly speed tests: what each
// tunnel carried, night after night, so a slow decline or an ISP's limit
// shows, and so the best path (package failover) can tell apart tunnels
// that ping and lose alike.
package speedlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"neotunnel/internal/config"
)

// Entry is one night's test of a tunnel.
type Entry struct {
	At     int64   `json:"at"`   // unix seconds
	Down   float64 `json:"down"` // bits/s
	Up     float64 `json:"up"`
	PingMs float64 `json:"ping_ms"`
	Loss   float64 `json:"loss"` // percent
	Err    string  `json:"error,omitempty"`
}

// keep is how many nights are kept per tunnel.
const keep = 60

var mu sync.Mutex

func path() string { return filepath.Join(config.Dir, "speedlog.json") }

func load() map[string][]Entry {
	m := map[string][]Entry{}
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// Add records a tunnel's test and forgets tunnels not in names (nil = keep
// all).
func Add(tunnel string, e Entry, names []string) error {
	mu.Lock()
	defer mu.Unlock()
	m := load()
	m[tunnel] = append(m[tunnel], e)
	if n := len(m[tunnel]); n > keep {
		m[tunnel] = m[tunnel][n-keep:]
	}
	if names != nil {
		live := map[string]bool{}
		for _, n := range names {
			live[n] = true
		}
		for n := range m {
			if !live[n] {
				delete(m, n)
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path())
}

// Of is a tunnel's tests, oldest first.
func Of(tunnel string) []Entry {
	mu.Lock()
	defer mu.Unlock()
	return load()[tunnel]
}

// Latest is each tunnel's download speed in its last good test (Mbit/s).
func Latest() map[string]float64 {
	mu.Lock()
	defer mu.Unlock()
	out := map[string]float64{}
	for n, es := range load() {
		for i := len(es) - 1; i >= 0; i-- {
			if es[i].Err == "" && es[i].Down > 0 {
				out[n] = es[i].Down / 1e6
				break
			}
		}
	}
	return out
}
