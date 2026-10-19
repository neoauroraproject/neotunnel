// Package metrics keeps the history behind the panel's graphs: CPU,
// memory, the network card, and every tunnel's and port's rate and ping.
// The daemon records a sample every 10 seconds into five rings of
// increasing resolution, from 10-second points for the last hour to one
// point a day for the last three years. Each point keeps the average and
// the peak of its samples, so a month-long graph still shows the short
// spikes. Points with no samples (the service was stopped) stay empty and
// show as gaps rather than zeros.
package metrics

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
)

// Every is how often the daemon records a sample.
const Every = 10 * time.Second

// tier is one ring: a point every step seconds, n points.
type tier struct{ step, n int64 }

var tiers = [...]tier{
	{10, 360},     // last hour
	{60, 1440},    // last day
	{600, 1008},   // last week
	{3600, 2160},  // last 90 days
	{86400, 1095}, // last 3 years
}

// Point is the samples of one interval. Each of the two values (rx and tx,
// or ping and loss) has its own coverage, so one can be missing.
type Point struct {
	At     uint32  // unix start of the interval; another value means an old lap
	TA, TB float32 // seconds covered by samples of A and B
	A, B   float32 // value × seconds, summed
	MA, MB float32 // highest sample
}

// Series is one measured thing at every resolution.
type Series struct {
	Since int64 // unix of its first sample
	Rings [len(tiers)][]Point
}

// Store holds every series, safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	series map[string]*Series
	path   string
	dirty  bool
}

func path() string { return filepath.Join(config.Dir, "metrics.gob.gz") }

// Open loads the saved history, or starts empty.
func Open() *Store { return openAt(path()) }

func openAt(p string) *Store {
	s := &Store{series: map[string]*Series{}, path: p}
	if f, err := os.Open(s.path); err == nil {
		defer f.Close()
		if zr, err := gzip.NewReader(f); err == nil {
			var m map[string]*Series
			if gob.NewDecoder(zr).Decode(&m) == nil {
				for k, v := range m {
					if v.valid() {
						s.series[k] = v
					}
				}
			}
		}
	}
	return s
}

func (s *Series) valid() bool {
	for i, r := range s.Rings {
		if int64(len(r)) != tiers[i].n {
			return false
		}
	}
	return true
}

// offset aligns intervals to local time, so a day is a calendar day here.
func offset(now time.Time) int64 {
	_, off := now.Zone()
	return int64(off)
}

// start is the unix start of the interval of t that contains now.
func (t tier) start(unix, off int64) int64 {
	return floorDiv(unix+off, t.step)*t.step - off
}

func (t tier) slot(start, off int64) int64 {
	return floorDiv(start+off, t.step) % t.n
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Record adds one sample of key covering dt before now. NaN leaves a
// value out (a ping that got no reply has a loss but no round trip).
func (s *Store) Record(key string, now time.Time, dt time.Duration, a, b float64) {
	sec := float32(dt.Seconds())
	if sec <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.series[key]
	if se == nil {
		se = &Series{Since: now.Unix()}
		for i, t := range tiers {
			se.Rings[i] = make([]Point, t.n)
		}
		s.series[key] = se
	}
	unix, off := now.Unix(), offset(now)
	for i, t := range tiers {
		st := t.start(unix, off)
		p := &se.Rings[i][t.slot(st, off)]
		if int64(p.At) != st {
			*p = Point{At: uint32(st)}
		}
		if !math.IsNaN(a) {
			p.TA += sec
			p.A += float32(a) * sec
			p.MA = max(p.MA, float32(a))
		}
		if !math.IsNaN(b) {
			p.TB += sec
			p.B += float32(b) * sec
			p.MB = max(p.MB, float32(b))
		}
	}
	s.dirty = true
}

// Keep drops the series of tunnels and ports that no longer exist: keys
// with a ':' whose name part isn't in names.
func (s *Store) Keep(names map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.series {
		if i := strings.IndexByte(k, ':'); i > 0 && !names[k[i+1:]] {
			delete(s.series, k)
			s.dirty = true
		}
	}
}

// Save writes the history if it changed.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	err := gob.NewEncoder(zw).Encode(s.series)
	s.dirty = false
	s.mu.Unlock()
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Ranges the panel offers, with the ring each reads and how many points.
var ranges = map[string]struct {
	tier int
	span time.Duration
}{
	"1h":  {0, time.Hour},
	"6h":  {1, 6 * time.Hour},
	"24h": {1, 24 * time.Hour},
	"7d":  {2, 7 * 24 * time.Hour},
	"30d": {3, 30 * 24 * time.Hour},
	"90d": {3, 90 * 24 * time.Hour},
	"all": {4, 0},
}

// ValidRange reports whether r names a range.
func ValidRange(r string) bool { _, ok := ranges[r]; return ok }

// maxPoints is the most points a graph gets; longer ranges merge
// neighbouring intervals (averages weighted, peaks kept).
const maxPoints = 720

// View is a range of history ready to draw: Start is the first point's
// time and Step the seconds between points.
type View struct {
	Range  string          `json:"range"`
	Step   int64           `json:"step"`
	Start  int64           `json:"start"`
	Points int             `json:"points"`
	Series map[string]Line `json:"series"`
}

// Line is one series over a view. A and B are averages per point, PA and
// PB the peaks; empty points are null. Sum is value × seconds over the
// whole range (for a rate in bits/s, Sum/8 is bytes).
type Line struct {
	Since int64      `json:"since"`
	A     Values     `json:"a"`
	B     Values     `json:"b"`
	PA    Values     `json:"pa"`
	PB    Values     `json:"pb"`
	Sum   [2]float64 `json:"sum"`
	Avg   [2]float64 `json:"avg"`
	Peak  [2]float64 `json:"peak"`
}

// Values are numbers where NaN is written as null.
type Values []float64

func (v Values) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(v)*6+2)
	b = append(b, '[')
	for i, x := range v {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b = append(b, "null"...)
			continue
		}
		b = strconv.AppendFloat(b, x, 'f', prec(x), 64)
	}
	return append(b, ']'), nil
}

// prec keeps three significant digits after the point for small values
// and none for large ones, which is all a graph needs.
func prec(x float64) int {
	switch a := math.Abs(x); {
	case a >= 100:
		return 0
	case a >= 1:
		return 2
	}
	return 3
}

// Query returns keys (all series when empty) over a range ending now.
func (s *Store) Query(rng string, keys []string, now time.Time) View {
	r, ok := ranges[rng]
	if !ok {
		rng, r = "24h", ranges["24h"]
	}
	t := tiers[r.tier]
	unix, off := now.Unix(), offset(now)
	last := t.start(unix, off)
	n := t.n
	if r.span > 0 {
		n = min(n, int64(r.span.Seconds())/t.step)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(keys) == 0 {
		for k := range s.series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	if rng == "all" {
		// From the oldest of the series asked for, at least two weeks.
		oldest := unix
		for _, k := range keys {
			if se := s.series[k]; se != nil && se.Since < oldest {
				oldest = se.Since
			}
		}
		n = min(t.n, max(14, (last-t.start(oldest, off))/t.step+1))
	}
	merge := (n + maxPoints - 1) / maxPoints
	points := (n + merge - 1) / merge
	first := last - (points*merge-1)*t.step
	v := View{Range: rng, Step: t.step * merge, Start: first, Points: int(points), Series: map[string]Line{}}
	for _, k := range keys {
		se := s.series[k]
		if se == nil {
			continue
		}
		ring := se.Rings[r.tier]
		l := Line{Since: se.Since, A: make(Values, points), B: make(Values, points), PA: make(Values, points), PB: make(Values, points)}
		var cov [2]float64
		for i := int64(0); i < points; i++ {
			var m Point
			for j := int64(0); j < merge; j++ {
				st := first + (i*merge+j)*t.step
				p := ring[t.slot(st, off)]
				if int64(p.At) != st {
					continue
				}
				m.TA += p.TA
				m.TB += p.TB
				m.A += p.A
				m.B += p.B
				m.MA = max(m.MA, p.MA)
				m.MB = max(m.MB, p.MB)
			}
			l.A[i], l.PA[i] = avg(m.A, m.TA), peak(m.MA, m.TA)
			l.B[i], l.PB[i] = avg(m.B, m.TB), peak(m.MB, m.TB)
			l.Sum[0] += float64(m.A)
			l.Sum[1] += float64(m.B)
			cov[0] += float64(m.TA)
			cov[1] += float64(m.TB)
			l.Peak[0] = max(l.Peak[0], float64(m.MA))
			l.Peak[1] = max(l.Peak[1], float64(m.MB))
		}
		for c := range cov {
			if cov[c] > 0 {
				l.Avg[c] = l.Sum[c] / cov[c]
			}
		}
		v.Series[k] = l
	}
	return v
}

func avg(sum, t float32) float64 {
	if t <= 0 {
		return math.NaN()
	}
	return float64(sum / t)
}

func peak(m, t float32) float64 {
	if t <= 0 {
		return math.NaN()
	}
	return float64(m)
}

// Keys lists the recorded series, sorted.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.series))
	for k := range s.series {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
