package transport

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Counters are a tunnel's bytes on the wire, over all its sessions; they
// outlive the sessions (and the changes that replace them).
type Counters struct {
	Rx, Tx atomic.Uint64
}

// limiter is a token bucket on what a tunnel sends; rate 0 is no limit.
type limiter struct {
	mu     sync.Mutex
	rate   float64 // bytes a second
	tokens float64
	at     time.Time
}

func (l *limiter) set(mbit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = float64(mbit) * 125000
	l.tokens, l.at = 0, time.Now()
}

// burst is what the bucket holds: 50 ms of the rate, at least 64 KiB.
func (l *limiter) burst() float64 { return max(l.rate/20, 64<<10) }

// wait takes n bytes from the bucket, sleeping until it has them.
func (l *limiter) wait(n int) {
	l.mu.Lock()
	if l.rate == 0 {
		l.mu.Unlock()
		return
	}
	now := time.Now()
	l.tokens = min(l.tokens+now.Sub(l.at).Seconds()*l.rate, l.burst())
	l.at = now
	l.tokens -= float64(n)
	var d time.Duration
	if l.tokens < 0 {
		d = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
}

// meter counts a session socket's bytes and limits what it sends.
type meter struct {
	net.Conn
	ctr atomic.Pointer[Counters]
	lim atomic.Pointer[limiter]
}

func newMeter(c net.Conn) *meter { return &meter{Conn: c} }

// attach sets whose counters and limit the socket goes on (a listener
// knows only after the handshake).
func (m *meter) attach(c *Counters, l *limiter) {
	m.ctr.Store(c)
	m.lim.Store(l)
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.Conn.Read(p)
	if c := m.ctr.Load(); c != nil && n > 0 {
		c.Rx.Add(uint64(n))
	}
	return n, err
}

func (m *meter) Write(p []byte) (int, error) {
	if l := m.lim.Load(); l != nil {
		l.wait(len(p))
	}
	n, err := m.Conn.Write(p)
	if c := m.ctr.Load(); c != nil && n > 0 {
		c.Tx.Add(uint64(n))
	}
	return n, err
}

var (
	notedMu sync.Mutex
	noted   = map[string]time.Time{}
)

// Note logs a problem of tunnel's, at most once a minute for each kind of
// problem, so a busy tunnel's failures show without flooding the log.
func Note(tunnel, format string, args ...any) {
	key := tunnel + "|" + format
	notedMu.Lock()
	last, seen := noted[key]
	now := time.Now()
	if seen && now.Sub(last) < time.Minute {
		notedMu.Unlock()
		return
	}
	noted[key] = now
	notedMu.Unlock()
	log.Printf("tunnel %s: %s", tunnel, fmt.Sprintf(format, args...))
}
