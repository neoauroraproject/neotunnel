package transport

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"

	"neotunnel/internal/config"
	"neotunnel/internal/tcptun"
)

// Handler carries one relay stream the far end opened on spec's tunnel.
type Handler func(spec Spec, c net.Conn)

// Hub runs this server's connection tunnels.
type Hub struct {
	mu     sync.Mutex
	groups map[string]*group
	ctrs   map[string]*Counters // by tunnel name, kept across changes
	lims   map[string]*limiter
	// bench are the temporary tunnels of speed tests, which Sync leaves be.
	bench map[string]*group

	handler atomic.Pointer[Handler]
	// probe is where streamProbe streams go: this server's probe server.
	probe atomic.Pointer[string]

	certOnce sync.Once
	tlsConf  *tls.Config
	certErr  error

	BadHandshakes atomic.Uint64 // sessions that failed to authenticate
}

func NewHub() *Hub {
	return &Hub{groups: map[string]*group{}, ctrs: map[string]*Counters{}, lims: map[string]*limiter{}, bench: map[string]*group{}}
}

// SetHandler sets what carries the relay streams the far ends open.
func (h *Hub) SetHandler(fn Handler) { h.handler.Store(&fn) }

// SetProbe sets where the far ends' speed test streams go (host:port).
func (h *Hub) SetProbe(addr string) { h.probe.Store(&addr) }

// Sync runs exactly these tunnels. A tunnel whose sessions must change gets
// new ones before the old ones go (see group.handOver).
func (h *Hub) Sync(specs []Spec) []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var errs []error
	want := map[string]bool{}
	for _, s := range specs {
		if err := check(s); err != nil {
			errs = append(errs, fmt.Errorf("tunnel %s: %w", s.Name, err))
			continue
		}
		want[s.Name] = true
		if h.ctrs[s.Name] == nil {
			h.ctrs[s.Name], h.lims[s.Name] = &Counters{}, &limiter{}
		}
		lim := h.lims[s.Name]
		lim.set(s.RateMbit)
		cur := h.groups[s.Name]
		if cur != nil && sameSessions(cur.spec, s) {
			continue
		}
		next := newGroup(h, s, h.ctrs[s.Name], lim)
		h.groups[s.Name] = next
		if cur != nil {
			go cur.handOver(next)
		}
	}
	for name, g := range h.groups {
		if !want[name] {
			g.close()
			delete(h.groups, name)
		}
	}
	return errs
}

func check(s Spec) error {
	if s.Kind != config.KindStreamTCP && s.Kind != config.KindStreamTLS {
		return fmt.Errorf("unknown connection tunnel kind %q", s.Kind)
	}
	if s.Dial && !s.Remote.IsValid() {
		return errors.New("the far end has no address")
	}
	if len(s.PortList()) == 0 {
		return errors.New("the tunnel has no port")
	}
	return tcptun.ValidCipher(s.Cipher)
}

// Close ends every tunnel.
func (h *Hub) Close() {
	h.Sync(nil)
	h.mu.Lock()
	var names []string
	for n := range h.bench {
		names = append(names, n)
	}
	h.mu.Unlock()
	h.StopBench(names)
}

// StartBench runs a temporary tunnel for a speed test, apart from the ones
// Sync keeps, until StopBench. Its name must not be a real tunnel's.
func (h *Hub) StartBench(s Spec) error {
	if err := check(s); err != nil {
		return err
	}
	h.StopBench([]string{s.Name})
	h.mu.Lock()
	h.bench[s.Name] = newGroup(h, s, &Counters{}, &limiter{})
	h.mu.Unlock()
	return nil
}

// StopBench ends the temporary tunnels of these names.
func (h *Hub) StopBench(names []string) {
	h.mu.Lock()
	var stop []*group
	for _, n := range names {
		if g, ok := h.bench[n]; ok {
			stop = append(stop, g)
			delete(h.bench, n)
		}
	}
	h.mu.Unlock()
	for _, g := range stop {
		g.close()
	}
}

func (h *Hub) group(name string) (*group, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.groups[name]
	if !ok {
		g, ok = h.bench[name]
	}
	return g, ok
}

// Open starts a relay stream to the far end of the tunnel named name.
func (h *Hub) Open(name string) (net.Conn, error) {
	g, ok := h.group(name)
	if !ok {
		return nil, fmt.Errorf("tunnel %s is not running here", name)
	}
	return g.open(streamRelay)
}

// OpenProbe starts a stream to the far end's probe server over the tunnel
// named name, for a speed test.
func (h *Hub) OpenProbe(name string) (net.Conn, error) {
	g, ok := h.group(name)
	if !ok {
		return nil, fmt.Errorf("tunnel %s is not running here", name)
	}
	return g.open(streamProbe)
}

// Ping measures a round trip to the far end over one of the sessions.
func (h *Hub) Ping(name string, timeout time.Duration) (time.Duration, error) {
	g, ok := h.group(name)
	if !ok {
		return 0, fmt.Errorf("tunnel %s is not running here", name)
	}
	return g.ping(timeout)
}

// State is a connection tunnel's live state.
type State struct {
	Sessions, Streams int
	Rx, Tx            uint64
}

// State reports name's state; ok is false when it doesn't run here.
func (h *Hub) State(name string) (State, bool) {
	h.mu.Lock()
	g, ok := h.groups[name]
	ctr := h.ctrs[name]
	h.mu.Unlock()
	if !ok {
		return State{}, false
	}
	var st State
	st.Sessions, st.Streams = g.up()
	if p := g.previous(); p != nil {
		s, n := p.up()
		st.Sessions, st.Streams = st.Sessions+s, st.Streams+n
	}
	st.Rx, st.Tx = ctr.Rx.Load(), ctr.Tx.Load()
	return st, true
}

// ListenPorts are the ports the tunnels that listen here need.
func (h *Hub) ListenPorts() []uint16 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []uint16
	for _, g := range h.all() {
		if g.spec.Dial {
			continue
		}
		for _, p := range g.spec.PortList() {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// all are the running tunnels, real and temporary; h.mu is held.
func (h *Hub) all() []*group {
	out := make([]*group, 0, len(h.groups)+len(h.bench))
	for _, g := range h.groups {
		out = append(out, g)
	}
	for _, g := range h.bench {
		out = append(out, g)
	}
	return out
}

// listening finds the listening tunnel a session names; from tells apart
// two of the same name (from two main servers).
func (h *Hub) listening(tunnel string, from netip.Addr) (*group, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var found []*group
	for _, g := range h.all() {
		if !g.spec.Dial && g.spec.Tunnel == tunnel {
			found = append(found, g)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	for _, g := range found {
		if g.spec.Remote == from {
			return g, true
		}
	}
	return nil, false
}

// ServePlain serves a connection whose first bytes were "NTS": a session of
// a stream-tcp tunnel.
func (h *Hub) ServePlain(c *net.TCPConn) {
	m := newMeter(c)
	if !h.accept(m, m, c, nil, false) {
		c.Close()
	}
}

// ServeTLS serves a connection that starts a TLS handshake (first holds the
// bytes already read from it): a session of a stream-tls tunnel, or anyone
// else, who gets the web server.
func (h *Hub) ServeTLS(c *net.TCPConn, first []byte) {
	conf, err := h.serverConfig()
	if err != nil {
		c.Close()
		return
	}
	m := newMeter(&prefixed{Conn: c, pre: first})
	tc := tls.Server(m, conf)
	tc.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := tc.Handshake(); err != nil {
		c.Close()
		return
	}
	cs := tc.ConnectionState()
	ekm, err := cs.ExportKeyingMaterial(ekmLabel, nil, 32)
	if err != nil {
		c.Close()
		return
	}
	br := bufio.NewReaderSize(tc, 512)
	magic, err := br.Peek(3)
	if err != nil || string(magic) != "NTS" {
		Decoy(tc)
		tc.Close()
		return
	}
	br.Discard(3)
	// br may hold more than the preamble (the handshake's first message):
	// everything after reads through it.
	if !h.accept(&buffered{Conn: tc, r: br}, m, c, ekm, true) {
		tc.Close()
	}
}

// accept authenticates a session on conn (its "NTS" read) and adds it to
// its tunnel. m counts the socket; raw is the TCP connection under it.
func (h *Hub) accept(conn net.Conn, m *meter, raw *net.TCPConn, ekm []byte, isTLS bool) bool {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	tunnel, cipher, err := readPreamble(conn)
	if err != nil {
		h.BadHandshakes.Add(1)
		return false
	}
	var from netip.Addr
	if a, ok := raw.RemoteAddr().(*net.TCPAddr); ok {
		from = a.AddrPort().Addr().Unmap()
	}
	g, ok := h.listening(tunnel, from)
	if !ok {
		h.BadHandshakes.Add(1)
		return false
	}
	s := g.spec
	if (s.Kind == config.KindStreamTLS) != isTLS || (!isTLS && cipher != s.Cipher) || (isTLS && cipher != "") {
		h.BadHandshakes.Add(1)
		return false
	}
	k, err := tcptun.NewStreamKeys(purpose(s.Kind, cipher, ekm), s.Secret, s.Tunnel, cipher, s.Side)
	if err != nil {
		return false
	}
	sc, err := k.Accept(conn)
	if err != nil {
		h.BadHandshakes.Add(1)
		return false
	}
	conn.SetDeadline(time.Time{})
	tcptun.Tune(raw, notSentLowat)
	m.attach(g.ctr, g.lim)
	ss, err := smux.Server(sc, smuxConfig())
	if err != nil {
		return false
	}
	g.add(ss)
	go func() {
		g.serve(ss)
		g.remove(ss)
	}()
	return true
}

// prefixed is a connection with bytes already read from it put back.
type prefixed struct {
	net.Conn
	pre []byte
}

func (p *prefixed) Read(b []byte) (int, error) {
	if len(p.pre) > 0 {
		n := copy(b, p.pre)
		p.pre = p.pre[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// buffered reads through r, which holds what was peeked at.
type buffered struct {
	net.Conn
	r *bufio.Reader
}

func (b *buffered) Read(p []byte) (int, error) { return b.r.Read(p) }
