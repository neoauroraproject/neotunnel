// Package events keeps a short history of what happened to each tunnel:
// health changes, restarts, moves to a backup and back, coming up, errors,
// and changes made from the panel or the terminal. Every process of the
// node (the daemon, the panel inside it, the CLI) appends to one file, one
// JSON object per line; appends of a line this small are atomic, so they
// never interleave. The daemon trims the file now and then.
package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"neotunnel/internal/config"
)

// Kinds of events.
const (
	Health   = "health"   // From → To grade (ok/degraded vs bad/down), Loss
	Restart  = "restart"  // Auto or by hand; Detail: why, or the far end's error
	Failover = "failover" // forwards (Detail: their ports) moved From → To
	// Cipher: automatic encryption moved From → To ("" = none, a cipher or
	// "obfs"); with Detail, it couldn't and stayed, and that says why.
	Cipher  = "cipher"
	Up      = "up"    // the interface was (re)created
	Error   = "error" // Detail: the error
	Created = "created"
	Changed = "changed" // Detail: what
	On      = "on"
	Off     = "off"
	Quota   = "quota" // To: "blocked" or "back"
	// Port: a sold port (Detail: its ports) closed, To: why (paused,
	// expired, quota), or "open" again.
	Port = "port"
	// Guard: the firewall turned itself off (Tunnel is empty).
	Guard = "guard"
	// Upgrade: this server upgraded the server From to version To
	// automatically; Detail: ok, failed: why, or rolled back (it went back
	// to its old version by itself). Tunnel is empty.
	Upgrade = "upgrade"
)

// Event is one thing that happened to a tunnel.
type Event struct {
	At     int64   `json:"at"` // unix seconds
	Tunnel string  `json:"tunnel"`
	Kind   string  `json:"kind"`
	From   string  `json:"from,omitempty"`
	To     string  `json:"to,omitempty"`
	Loss   float64 `json:"loss,omitempty"`
	Auto   bool    `json:"auto,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

const (
	// maxBytes is when the daemon trims the file, to keepLines.
	maxBytes  = 2 << 20
	keepLines = 5000
)

var (
	mu sync.Mutex
	// hooks hear every event added in this process (the daemon sends
	// alerts and webhooks from them).
	hooks   atomic.Pointer[[]func(Event)]
	hooksMu sync.Mutex
)

// OnAdd adds a function that hears every event added in this process.
func OnAdd(f func(Event)) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	var hs []func(Event)
	if cur := hooks.Load(); cur != nil {
		hs = append(hs, *cur...)
	}
	hs = append(hs, f)
	hooks.Store(&hs)
}

func path() string { return filepath.Join(config.Dir, "events.log") }

// Add records an event now.
func Add(e Event) {
	if e.At == 0 {
		e.At = time.Now().Unix()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
	f.Close()
	if hs := hooks.Load(); hs != nil {
		for _, h := range *hs {
			go h(e)
		}
	}
}

// Of returns a tunnel's events, newest first, at most limit.
func Of(tunnel string, limit int) []Event {
	b, err := os.ReadFile(path())
	if err != nil {
		return nil
	}
	var all []Event
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Tunnel == tunnel {
			all = append(all, e)
		}
	}
	out := make([]Event, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out
}

// Trim keeps the newest keepLines lines once the file passes maxBytes.
func Trim() {
	mu.Lock()
	defer mu.Unlock()
	fi, err := os.Stat(path())
	if err != nil || fi.Size() < maxBytes {
		return
	}
	b, err := os.ReadFile(path())
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) > keepLines {
		lines = lines[len(lines)-keepLines:]
	}
	tmp := path() + ".tmp"
	if os.WriteFile(tmp, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600) == nil {
		os.Rename(tmp, path())
	}
}

// Class folds a health grade into what the log tells apart: "ok" (ok or
// degraded) or "bad" or "down", so small loss moving up and down doesn't
// fill the log.
func Class(grade string) string {
	switch grade {
	case "bad", "down":
		return grade
	case "":
		return ""
	}
	return "ok"
}
