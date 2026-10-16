// Package traffic keeps per-tunnel traffic totals that survive interface
// re-creation and reboots: the daemon samples the kernel counters, adds the
// deltas to running totals, daily totals and a 24-hour series of 5-minute
// buckets, and saves them in traffic.json. Speed tests are moved out of the
// totals, so they show what users really used.
package traffic

import (
	"encoding/json"
	"fmt"
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

	// NIC is the counter name of this node's public network interface: the
	// traffic a datacenter bills.
	NIC = "@nic"
	// Port prefixes a forward's per-port counter name ("@port:tcp/443").
	Port = "@port:"
)

// Pair is received and sent bytes, with packet counts (to estimate the
// bytes on the wire, encapsulation included).
type Pair struct {
	Rx  uint64 `json:"rx"`
	Tx  uint64 `json:"tx"`
	RxP uint64 `json:"rxp,omitempty"`
	TxP uint64 `json:"txp,omitempty"`
}

func (p *Pair) add(d Pair) { p.Rx += d.Rx; p.Tx += d.Tx; p.RxP += d.RxP; p.TxP += d.TxP }

// sub takes d away, never below zero, and returns what it couldn't take.
func (p *Pair) sub(d Pair) Pair {
	take := func(a *uint64, b uint64) uint64 {
		if *a >= b {
			*a -= b
			return 0
		}
		left := b - *a
		*a = 0
		return left
	}
	return Pair{Rx: take(&p.Rx, d.Rx), Tx: take(&p.Tx, d.Tx), RxP: take(&p.RxP, d.RxP), TxP: take(&p.TxP, d.TxP)}
}

func (p Pair) zero() bool { return p == Pair{} }

// Counter is one tunnel's accounting.
type Counter struct {
	Since  time.Time       `json:"since"`
	Total  Pair            `json:"total"`
	Tests  Pair            `json:"tests,omitempty"` // speed tests, kept out of the totals
	Days   map[string]Pair `json:"days"`
	Series []Pair          `json:"series"` // Buckets entries, oldest first
	Head   int64           `json:"head"`   // unix start of the newest bucket

	// Last raw kernel counters, to compute deltas.
	Last Pair `json:"last"`
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

// Sample records the current kernel counters of an interface. A counter
// that went backwards means the interface was re-created (or the machine
// rebooted): the new value is all new traffic.
func (s *Store) Sample(tunnel string, cur Pair, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Tunnels[tunnel]
	if c == nil {
		s.Tunnels[tunnel] = &Counter{Since: now, Days: map[string]Pair{}, Last: cur}
		s.dirty = true
		return
	}
	l := c.Last
	d := cur
	if cur.Rx >= l.Rx && cur.Tx >= l.Tx && cur.RxP >= l.RxP && cur.TxP >= l.TxP {
		d = Pair{Rx: cur.Rx - l.Rx, Tx: cur.Tx - l.Tx, RxP: cur.RxP - l.RxP, TxP: cur.TxP - l.TxP}
	}
	c.Last = cur
	if d.zero() {
		return
	}
	c.Total.add(d)
	if c.Days == nil {
		c.Days = map[string]Pair{}
	}
	day := now.In(s.location).Format(dayFmt)
	dd := c.Days[day]
	dd.add(d)
	c.Days[day] = dd
	c.advance(now)
	c.Series[len(c.Series)-1].add(d)
	s.dirty = true
}

// Subtract moves traffic out of a tunnel's totals into its test counter:
// a speed test runs through the tunnel but isn't anyone's real traffic.
func (s *Store) Subtract(tunnel string, d Pair, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Tunnels[tunnel]
	if c == nil || d.zero() {
		return
	}
	c.Total.sub(d)
	c.Tests.add(d)
	day := now.In(s.location).Format(dayFmt)
	if dd, ok := c.Days[day]; ok {
		dd.sub(d)
		c.Days[day] = dd
	}
	left := d
	for i := len(c.Series) - 1; i >= 0 && !left.zero(); i-- {
		left = c.Series[i].sub(left)
	}
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

// Keep drops counters of tunnels that no longer exist (the NIC stays) and
// old days.
func (s *Store) Keep(tunnels []string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	for _, t := range tunnels {
		keep[t] = true
	}
	oldest := now.AddDate(0, 0, -keepDays).In(s.location).Format(dayFmt)
	for name, c := range s.Tunnels {
		if !keep[name] && !strings.HasPrefix(name, "@") {
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
			*c = Counter{Since: now, Days: map[string]Pair{}, Last: c.Last}
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
	Tests  Pair   `json:"tests"`
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
		sum := Summary{Since: c.Since.Unix(), Total: c.Total, Tests: c.Tests, Today: c.Days[today]}
		for d, p := range c.Days {
			if d[:7] == month {
				sum.Month.add(p)
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

func resetPath() string   { return filepath.Join(config.Dir, "traffic.reset") }
func excludePath() string { return filepath.Join(config.Dir, "traffic.exclude") }

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

// RequestExclude asks the daemon to move d of a tunnel's traffic (a speed
// test) out of its totals. Safe to call from any process.
func RequestExclude(tunnel string, d Pair) error {
	return config.WithLock(func() error {
		f, err := os.OpenFile(excludePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "%s %d %d %d %d\n", tunnel, d.Rx, d.Tx, d.RxP, d.TxP)
		return err
	})
}

// Exclude is one pending RequestExclude.
type Exclude struct {
	Tunnel string
	Pair
}

// TakeExcludes returns and clears the pending exclusions.
func TakeExcludes() []Exclude {
	var out []Exclude
	config.WithLock(func() error {
		b, err := os.ReadFile(excludePath())
		if err != nil {
			return nil
		}
		os.Remove(excludePath())
		for _, line := range strings.Split(string(b), "\n") {
			var e Exclude
			if n, _ := fmt.Sscanf(line, "%s %d %d %d %d", &e.Tunnel, &e.Rx, &e.Tx, &e.RxP, &e.TxP); n == 5 {
				out = append(out, e)
			}
		}
		return nil
	})
	return out
}
