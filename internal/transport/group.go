package transport

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

// errNoSession: no session is up to carry a stream.
var errNoSession = errors.New("the tunnel has no session up")

// group is one connection tunnel's sessions.
type group struct {
	hub  *Hub
	spec Spec
	ctr  *Counters
	lim  *limiter

	mu   sync.Mutex
	sess []*smux.Session
	// prev is the group this one replaced while that one still carries
	// connections: until this one has a session, new streams go there.
	prev *group

	stop     chan struct{}
	stopOnce sync.Once
	last     atomic.Int32 // the port that connected last, first next time
}

func newGroup(h *Hub, s Spec, ctr *Counters, lim *limiter) *group {
	g := &group{hub: h, spec: s, ctr: ctr, lim: lim, stop: make(chan struct{})}
	if s.Dial {
		for i := range max(1, s.Conns) {
			go g.keep(i)
		}
	}
	return g
}

func (g *group) stopped() bool {
	select {
	case <-g.stop:
		return true
	default:
		return false
	}
}

// keep keeps one session to the far end open, connecting again (on the
// ports in turn) whenever it ends.
func (g *group) keep(i int) {
	// Staggered, so the sessions don't all knock at once.
	if !g.sleep(time.Duration(i) * 150 * time.Millisecond) {
		return
	}
	backoff := time.Second
	failing := false
	for !g.stopped() {
		s, err := g.dial()
		if err != nil {
			if !failing && i == 0 {
				log.Printf("tunnel %s: %v", g.spec.Name, err)
			}
			failing = true
			if !g.sleep(backoff) {
				return
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		if failing && i == 0 {
			log.Printf("tunnel %s: connected", g.spec.Name)
		}
		failing, backoff = false, time.Second
		g.add(s)
		g.serve(s)
		g.remove(s)
		if !g.sleep(200 * time.Millisecond) {
			return
		}
	}
}

func (g *group) sleep(d time.Duration) bool {
	if d <= 0 {
		return !g.stopped()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-g.stop:
		return false
	case <-t.C:
		return true
	}
}

// dial opens a session on the first port that takes it, starting with the
// one that worked last.
func (g *group) dial() (*smux.Session, error) {
	ports := g.spec.PortList()
	if len(ports) == 0 {
		return nil, errors.New("the tunnel has no port")
	}
	start := int(g.last.Load()) % len(ports)
	var err error
	for k := range ports {
		i := (start + k) % len(ports)
		timeout := dialTimeout
		if k < len(ports)-1 {
			timeout = fallbackTimeout
		}
		var s *smux.Session
		if s, err = g.dialPort(ports[i], timeout); err == nil {
			g.last.Store(int32(i))
			return s, nil
		}
		if g.stopped() {
			break
		}
	}
	return nil, err
}

func (g *group) add(s *smux.Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sess = append(g.sess, s)
	// A listener keeps what the far end opens, within reason: the oldest
	// goes when it keeps opening more (it gave up on them).
	if limit := 4 * max(1, g.spec.Conns); !g.spec.Dial && len(g.sess) > limit {
		g.sess[0].Close()
		g.sess = g.sess[1:]
	}
}

func (g *group) remove(s *smux.Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, x := range g.sess {
		if x == s {
			g.sess = append(g.sess[:i], g.sess[i+1:]...)
			break
		}
	}
	s.Close()
}

// serve takes the streams the far end opens on s until s ends.
func (g *group) serve(s *smux.Session) {
	for {
		st, err := s.AcceptStream()
		if err != nil {
			return
		}
		go g.stream(st)
	}
}

func (g *group) stream(st *smux.Stream) {
	st.SetReadDeadline(time.Now().Add(dialTimeout))
	var t [1]byte
	if _, err := io.ReadFull(st, t[:]); err != nil {
		Note(g.spec.Name, "a stream from the far end closed before its type: %v", err)
		st.Close()
		return
	}
	st.SetReadDeadline(time.Time{})
	switch t[0] {
	case streamRelay:
		if h := g.hub.handler.Load(); h != nil {
			(*h)(g.spec, &Stream{st})
			return
		}
	case streamPing:
		st.Write(t[:])
	case streamProbe:
		if a := g.hub.probe.Load(); a != nil {
			probeTo(*a, &Stream{st})
			return
		}
	}
	st.Close()
}

// probeTo carries a speed test's stream to the probe server at addr, both
// ways, passing on each side's end of data.
func probeTo(addr string, st *Stream) {
	defer st.Close()
	c, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return
	}
	defer c.Close()
	done := make(chan struct{})
	go func() {
		io.Copy(c, st)
		c.(*net.TCPConn).CloseWrite()
		close(done)
	}()
	io.Copy(st, c)
	st.CloseWrite()
	<-done
}

// pick is the live session with the fewest streams.
func (g *group) pick() *smux.Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	var best *smux.Session
	for _, s := range g.sess {
		if s.IsClosed() {
			continue
		}
		if best == nil || s.NumStreams() < best.NumStreams() {
			best = s
		}
	}
	return best
}

// Stream is one stream of a session as a net.Conn that half-closes.
type Stream struct{ *smux.Stream }

// WriteTo ends cleanly at the far end's end of data (smux reports io.EOF,
// which a copy would take for a broken connection).
func (s *Stream) WriteTo(w io.Writer) (int64, error) {
	n, err := s.Stream.WriteTo(w)
	if err == io.EOF {
		err = nil
	}
	return n, err
}

// open starts a stream of type t, waiting a little for a session when none
// is up (one is on its way after a change or a cut).
func (g *group) open(t byte) (*Stream, error) {
	deadline := time.Now().Add(openWait)
	for {
		s := g.pick()
		if s == nil {
			if p := g.previous(); p != nil {
				s = p.pick()
			}
		}
		if s != nil {
			st, err := s.OpenStream()
			if err == nil {
				if _, err = st.Write([]byte{t}); err == nil {
					return &Stream{st}, nil
				}
				st.Close()
			}
			if time.Now().After(deadline) {
				return nil, err
			}
		} else if time.Now().After(deadline) || g.stopped() {
			return nil, errNoSession
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (g *group) previous() *group {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prev
}

// ping measures one round trip over a session.
func (g *group) ping(timeout time.Duration) (time.Duration, error) {
	st, err := g.open(streamPing)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	start := time.Now()
	st.SetReadDeadline(start.Add(timeout))
	var b [1]byte
	if _, err := io.ReadFull(st, b[:]); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// up reports the live sessions and the streams on them.
func (g *group) up() (sessions, streams int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.sess {
		if !s.IsClosed() {
			sessions++
			streams += s.NumStreams()
		}
	}
	return sessions, streams
}

// close ends every session at once.
func (g *group) close() {
	g.stopOnce.Do(func() { close(g.stop) })
	g.mu.Lock()
	ss := g.sess
	g.sess, g.prev = nil, nil
	g.mu.Unlock()
	for _, s := range ss {
		s.Close()
	}
}

// handOver lets next take over: next carries new streams as soon as it has
// a session, g's sessions stop being replaced and each closes when its last
// stream does (or after drainMax).
func (g *group) handOver(next *group) {
	g.stopOnce.Do(func() { close(g.stop) })
	next.mu.Lock()
	next.prev = g
	next.mu.Unlock()
	start := time.Now()
	deadline := start.Add(drainMax)
	for {
		// Connect before cut: g's idle sessions stay until next has one of
		// its own (or clearly won't get one soon).
		nextUp, _ := next.up()
		cut := nextUp > 0 || time.Since(start) > 15*time.Second || next.stopped()
		g.mu.Lock()
		keep := g.sess[:0]
		for _, s := range g.sess {
			if s.IsClosed() || (cut && s.NumStreams() == 0) || time.Now().After(deadline) {
				s.Close()
			} else {
				keep = append(keep, s)
			}
		}
		g.sess = keep
		left := len(keep)
		g.mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(time.Second)
	}
	next.mu.Lock()
	if next.prev == g {
		next.prev = nil
	}
	next.mu.Unlock()
}
