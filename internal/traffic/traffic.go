// Package traffic keeps per-tunnel traffic totals that survive interface
// re-creation and reboots: the daemon samples the kernel counters, adds the
// deltas to running totals, daily totals and a 24-hour series of 5-minute
// buckets, and saves them in traffic.json.
package traffic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
)

const (
	Bucket   = 5 * time.Minute
	Buckets  = 288 // 24 hours
	keepDays = 62
	dayFmt   = "2006-01-02"
)

// Pair is received and sent bytes.
type Pair struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

func (p *Pair) add(rx, tx uint64) { p.Rx += rx; p.Tx += tx }

// Counter is one tunnel's accounting.
type Counter struct {
	Since  time.Time       `json:"since"`
	Total  Pair            `json:"total"`
	Days   map[string]Pair `json:"days"`
	Series []Pair          `json:"series"` // Buckets entries, oldest first
	Head   int64           `json:"head"`   // unix start of the newest bucket

	// Last raw kernel counters, to compute deltas.
	LastRx uint64 `json:"last_rx"`
	LastTx uint64 `json:"last_tx"`
	seen   bool
}

// Store is the set of counters, safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	Tunnels  map[string]*Counter `json:"tunnels"`
	dirty    bool
	path     string
	location *time.Location
}

func path() string { return filepath.Join(config.Dir, "traffic.json") }

// Open loads the saved counters (or starts empty).
func Open() *Store {
	s := &Store{Tunnels: map[string]*Counter{}, path: path(), location: time.Local}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, s)
		if s.Tunnels == nil {
			s.Tunnels = map[string]*Counter{}
		}
	}
	return s
}

// Sample records the current kernel counters of a tunnel interface. A
// counter that went backwards means the interface was re-created: the new
// value is all new traffic.
func (s *Store) Sample(tunnel string, rx, tx uint64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Tunnels[tunnel]
	if c == nil {
		c = &Counter{Since: now, Days: map[string]Pair{}}
		s.Tunnels[tunnel] = c
		c.LastRx, c.LastTx, c.seen = rx, tx, true
		s.dirty = true
		return
	}
	var drx, dtx uint64
	switch {
	case !c.seen && (rx < c.LastRx || tx < c.LastTx):
		// First sample after a restart and the interface is new (reboot).
		drx, dtx = rx, tx
	case rx >= c.LastRx && tx >= c.LastTx:
		drx, dtx = rx-c.LastRx, tx-c.LastTx
	default:
		drx, dtx = rx, tx
	}
	c.LastRx, c.LastTx, c.seen = rx, tx, true
	if drx == 0 && dtx == 0 {
		return
	}
	c.Total.add(drx, dtx)
	if c.Days == nil {
		c.Days = map[string]Pair{}
	}
	day := now.In(s.location).Format(dayFmt)
	d := c.Days[day]
	d.add(drx, dtx)
	c.Days[day] = d
	c.advance(now)
	c.Series[len(c.Series)-1].add(drx, dtx)
	s.dirty = true
}

// advance rolls the series forward so its last bucket covers now.
func (c *Counter) advance(now time.Time) {
	head := now.Truncate(Bucket).Unix()
	if len(c.Series) != Buckets {
		c.Series = make([]Pair, Buckets)
		c.Head = head
		return
	}
	steps := (head - c.Head) / int64(Bucket.Seconds())
	switch {
	case steps <= 0:
		return
	case steps >= Buckets:
		clear(c.Series)
	default:
		copy(c.Series, c.Series[steps:])
		clear(c.Series[Buckets-int(steps):])
	}
	c.Head = head
}

// Keep drops counters of tunnels that no longer exist and old days.
func (s *Store) Keep(tunnels []string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	for _, t := range tunnels {
		keep[t] = true
	}
	oldest := now.AddDate(0, 0, -keepDays).In(s.location).Format(dayFmt)
	for name, c := range s.Tunnels {
		if !keep[name] {
			delete(s.Tunnels, name)
			s.dirty = true
			continue
		}
		for d := range c.Days {
			if d < oldest {
				delete(c.Days, d)
				s.dirty = true
			}
		}
	}
}

// Reset zeroes one tunnel's counters, or all when tunnel is "".
func (s *Store) Reset(tunnel string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, c := range s.Tunnels {
		if tunnel == "" || name == tunnel {
			*c = Counter{Since: now, Days: map[string]Pair{}, LastRx: c.LastRx, LastTx: c.LastTx, seen: c.seen}
		}
	}
	s.dirty = true
}

// Save writes the counters if they changed.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Summary is what the panel shows for a tunnel.
type Summary struct {
	Since  int64  `json:"since"` // unix seconds
	Total  Pair   `json:"total"`
	Today  Pair   `json:"today"`
	Month  Pair   `json:"month"`
	Series []Pair `json:"series"` // last 24 hours, 5-minute buckets
	Days   []Day  `json:"days"`   // last 30 days, oldest first
}

type Day struct {
	Date string `json:"date"`
	Pair
}

// Summaries returns every tunnel's summary as of now.
func (s *Store) Summaries(now time.Time) map[string]Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Summary{}
	today := now.In(s.location).Format(dayFmt)
	month := today[:7]
	for name, c := range s.Tunnels {
		sum := Summary{Since: c.Since.Unix(), Total: c.Total, Today: c.Days[today]}
		for d, p := range c.Days {
			if d[:7] == month {
				sum.Month.add(p.Rx, p.Tx)
			}
		}
		// Series aligned to now: buckets older than the last sample are zero.
		sum.Series = make([]Pair, Buckets)
		if len(c.Series) == Buckets {
			shift := (now.Truncate(Bucket).Unix() - c.Head) / int64(Bucket.Seconds())
			if shift >= 0 && shift < Buckets {
				copy(sum.Series, c.Series[shift:])
			}
		}
		for i := 29; i >= 0; i-- {
			d := now.AddDate(0, 0, -i).In(s.location).Format(dayFmt)
			sum.Days = append(sum.Days, Day{Date: d, Pair: c.Days[d]})
		}
		out[name] = sum
	}
	return out
}

// Names lists tracked tunnels, sorted.
func (s *Store) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for n := range s.Tunnels {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func resetPath() string { return filepath.Join(config.Dir, "traffic.reset") }

// RequestReset asks the daemon, which owns the counters, to zero those of
// tunnel ("" = all) on its next sample.
func RequestReset(tunnel string) error {
	return os.WriteFile(resetPath(), []byte(tunnel), 0o600)
}

// TakeResetRequest returns and clears a pending reset request.
func TakeResetRequest() (string, bool) {
	b, err := os.ReadFile(resetPath())
	if err != nil {
		return "", false
	}
	os.Remove(resetPath())
	return strings.TrimSpace(string(b)), true
}
