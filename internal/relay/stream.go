package relay

// Relay mode carries a TCP forward without a packet tunnel: this node (the
// entry) accepts the user's connection and opens a TCP connection of its own
// to the far node (the exit), which connects onward to the service there.
// Every user connection is then an ordinary TCP connection on each leg, with
// its own congestion control, instead of TCP inside a tunnel's TCP stream
// or packets: on a long, lossy path one user's losses slow only that user.
//
// The exit listens on the tunnel's relay ports (config.Tunnel.RelayPorts:
// random ones, tried in order; RelayPort for tunnels from before them).
// Each connection starts with a short plain
// preamble naming the tunnel and cipher, then the TCP transport's mutual
// challenge-response (internal/tcptun) under keys derived from the tunnel's
// secret, so only the tunnel's other end can use it. With a cipher, the rest
// travels in AES-GCM or ChaCha20-Poly1305 frames. The entry keeps a few
// connections handshaken in advance, so a new user doesn't wait for it.
//
//	preamble  "NTR" version(1) len(1) tunnel len(1) cipher
//	handshake tcptun (purpose "relay <cipher>", so the cipher is authenticated)
//	request   port(2) proxy_version(1) [src ip(16) port(2) dst ip(16) port(2)]
//	          or, for a proxy: port(2) 0xff len(1) host, answered by status(1)
//
// then the user's bytes both ways. On a connection tunnel (Via.Session) the
// same request opens a stream on one of the tunnel's sessions instead (see
// internal/transport), with no preamble or handshake of its own.
//
// The same listeners take the connection tunnels' sessions ("NTS", or a TLS
// handshake) and answer anything else like a web server.
//
// The exit only connects to its own
// loopback address, on the ports the tunnel's relay forwards name; for a
// proxy (Exit.Egress) to host:port on the internet, never to its own
// loopback or private networks. status is a SOCKS5 reply code (0 = ok).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/tcptun"
	"neotunnel/internal/transport"
)

// RelayPort is where nodes accepted relay connections before each tunnel
// had ports of its own.
const RelayPort = config.LegacyRelayPort

const (
	streamVersion = 1
	// notSentLowat caps the unsent bytes each relay socket queues beyond
	// what is in flight.
	notSentLowat = 256 << 10
	// poolIdle is how long a ready connection waits for a user; the exit
	// waits a little longer before giving up on it.
	poolIdle     = 45 * time.Second
	exitIdle     = poolIdle + 30*time.Second
	maxBackoff   = 30 * time.Second
	maxWaiting   = 1024 // exit connections not yet carrying a user
	exitDialWait = 5 * time.Second
)

// Via is the far end of a relay-mode forward, seen from the entry.
type Via struct {
	Tunnel string
	Secret string
	Side   int            // the entry's side of the tunnel
	Exit   netip.AddrPort // the exit node's relay listener
	// Inside is the same listener at the far node's address inside the
	// tunnel: used when the public one can't be reached (a port filtered
	// between the servers).
	Inside netip.AddrPort
	// Ports, when set, are the ports to try on each of those addresses in
	// turn instead of theirs.
	Ports  [config.MaxRelayPorts]uint16
	Cipher string // "" = authenticated, not encrypted
	Pool   int    // connections kept ready
	// Session names a connection tunnel here: each user connection is a
	// stream on its sessions instead of a connection of its own.
	Session string
}

// candidates are the addresses to dial, in order: the public address on
// each port, then the inside one.
func (v Via) candidates() []netip.AddrPort {
	var ports []uint16
	for _, p := range v.Ports {
		if p != 0 {
			ports = append(ports, p)
		}
	}
	var out []netip.AddrPort
	for _, a := range []netip.AddrPort{v.Exit, v.Inside} {
		if !a.IsValid() {
			continue
		}
		if len(ports) == 0 {
			out = append(out, a)
			continue
		}
		for _, p := range ports {
			out = append(out, netip.AddrPortFrom(a.Addr(), p))
		}
	}
	return out
}

// Exit is a tunnel whose relay forwards end on this node.
type Exit struct {
	Tunnel string
	Secret string
	Side   int      // this node's side
	Ports  []uint16 // the local ports its forwards may reach
	// Egress: the far node runs a proxy over this tunnel, whose connections
	// leave to the internet from here.
	Egress bool
	// From are the far node's addresses: when two tunnels here have the same
	// name (from two main servers), a connection is matched by its source.
	From []netip.Addr
	// Listen are the ports the far node's relay connections come to (none
	// = RelayPort). Session: the tunnel is a connection tunnel, whose
	// relays come over its sessions instead.
	Listen  []uint16
	Session bool
	// Public are the ones of Ports to reach at Addr, this node's public
	// address, instead of its loopback: a service that listens there only.
	Public []uint16
	Addr   netip.Addr
}

// dialFor connects to the service a relay request names on this node.
func (e Exit) dialFor(port uint16) (*net.TCPConn, error) {
	if e.Addr.IsValid() && allowed(e.Public, port) {
		c, err := net.DialTimeout("tcp", netip.AddrPortFrom(e.Addr, port).String(), exitDialWait)
		if err != nil {
			return nil, err
		}
		return c.(*net.TCPConn), nil
	}
	return dialLocal(port)
}

func purpose(cipher string) string {
	if cipher == "" {
		cipher = "none"
	}
	return "relay " + cipher
}

// ---------- entry ----------

type pooled struct {
	c   net.Conn
	raw *net.TCPConn
	at  time.Time
}

// pool keeps connections to one exit handshaken and waiting.
type pool struct {
	via  Via
	keys *tcptun.StreamKeys

	mu   sync.Mutex
	idle []*pooled
	last atomic.Int32 // the candidate that connected last, tried first
	wake chan struct{}
	stop chan struct{}
	once sync.Once
}

func newPool(v Via) (*pool, error) {
	if err := tcptun.ValidCipher(v.Cipher); err != nil {
		return nil, err
	}
	k, err := tcptun.NewStreamKeys(purpose(v.Cipher), v.Secret, v.Tunnel, v.Cipher, v.Side)
	if err != nil {
		return nil, err
	}
	p := &pool{via: v, keys: k, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	go p.fill()
	return p, nil
}

func (p *pool) size() int { return max(1, min(p.via.Pool, 32)) }

func (p *pool) close() {
	p.once.Do(func() {
		close(p.stop)
		p.mu.Lock()
		for _, c := range p.idle {
			c.c.Close()
		}
		p.idle = nil
		p.mu.Unlock()
	})
}

func (p *pool) kick() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// fill keeps size() fresh connections ready, retiring old ones before the
// exit (or a NAT on the way) gives up on them.
func (p *pool) fill() {
	backoff := time.Second
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	failing := false
	for {
		p.mu.Lock()
		keep := p.idle[:0]
		for _, c := range p.idle {
			if time.Since(c.at) > poolIdle {
				c.c.Close()
			} else {
				keep = append(keep, c)
			}
		}
		p.idle = keep
		need := p.size() - len(p.idle)
		p.mu.Unlock()
		for ; need > 0; need-- {
			c, raw, err := p.dial()
			if err != nil {
				if !failing {
					log.Printf("relay %s: %v", p.via.Tunnel, err)
					failing = true
				}
				select {
				case <-p.stop:
					return
				case <-time.After(backoff):
				}
				backoff = min(2*backoff, maxBackoff)
				break
			}
			failing, backoff = false, time.Second
			p.mu.Lock()
			select {
			case <-p.stop:
				p.mu.Unlock()
				c.Close()
				return
			default:
			}
			p.idle = append(p.idle, &pooled{c: c, raw: raw, at: time.Now()})
			p.mu.Unlock()
		}
		select {
		case <-p.stop:
			return
		case <-p.wake:
		case <-t.C:
		}
	}
}

func (p *pool) dial() (net.Conn, *net.TCPConn, error) {
	// The way that worked last goes first; the others, in order, are the
	// fallbacks (a port filtered between the servers, the public address).
	ways := p.via.candidates()
	if len(ways) == 0 {
		return nil, nil, errors.New("the far end has no relay address")
	}
	start := int(p.last.Load()) % len(ways)
	var c net.Conn
	var err error
	for k := range ways {
		i := (start + k) % len(ways)
		d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
		if k < len(ways)-1 {
			d.Timeout = 3 * time.Second
		}
		if c, err = d.Dial("tcp", ways[i].String()); err == nil {
			p.last.Store(int32(i))
			break
		}
	}
	if c == nil {
		return nil, nil, err
	}
	raw := c.(*net.TCPConn)
	tcptun.Tune(raw, notSentLowat)
	pre := []byte{'N', 'T', 'R', streamVersion, byte(len(p.via.Tunnel))}
	pre = append(pre, p.via.Tunnel...)
	pre = append(pre, byte(len(p.via.Cipher)))
	pre = append(pre, p.via.Cipher...)
	if _, err := c.Write(pre); err != nil {
		c.Close()
		return nil, nil, err
	}
	sc, err := p.keys.Dial(c)
	if err != nil {
		c.Close()
		if errors.Is(err, tcptun.ErrAuth) {
			err = fmt.Errorf("%s does not hold this tunnel's secret", p.via.Exit)
		} else {
			err = fmt.Errorf("handshake with %s: %w", p.via.Exit, err)
		}
		return nil, nil, err
	}
	return sc, raw, nil
}

// get returns a ready connection, or dials one when none is.
func (p *pool) get() (net.Conn, error) {
	defer p.kick()
	for {
		p.mu.Lock()
		n := len(p.idle)
		if n == 0 {
			p.mu.Unlock()
			c, _, err := p.dial()
			return c, err
		}
		c := p.idle[n-1] // the freshest
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		if time.Since(c.at) < poolIdle && alive(c.raw) {
			return c.c, nil
		}
		c.c.Close()
	}
}

// request is what the entry asks of the exit for one user connection.
type request struct {
	port     uint16
	version  int
	src, dst netip.AddrPort
	host     string // a proxy's destination (version verConnect)
}

// verConnect in the version byte asks the exit to connect to a host.
const verConnect = 0xff

func (r request) marshal() []byte {
	b := binary.BigEndian.AppendUint16(nil, r.port)
	if r.host != "" {
		b = append(b, verConnect, byte(len(r.host)))
		return append(b, r.host...)
	}
	b = append(b, byte(r.version))
	if r.version != 0 {
		for _, ap := range []netip.AddrPort{r.src, r.dst} {
			a := ap.Addr().As16()
			b = append(b, a[:]...)
			b = binary.BigEndian.AppendUint16(b, ap.Port())
		}
	}
	return b
}

func readRequest(r io.Reader) (request, error) {
	var h [3]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return request{}, err
	}
	q := request{port: binary.BigEndian.Uint16(h[:2]), version: int(h[2])}
	switch q.version {
	case 0:
		return q, nil
	case 1, 2:
	case verConnect:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil || n[0] == 0 {
			return q, errors.New("bad proxy request")
		}
		host := make([]byte, n[0])
		if _, err := io.ReadFull(r, host); err != nil {
			return q, err
		}
		q.host = string(host)
		return q, nil
	default:
		return q, fmt.Errorf("bad PROXY protocol version %d", q.version)
	}
	var a [36]byte
	if _, err := io.ReadFull(r, a[:]); err != nil {
		return q, err
	}
	q.src = netip.AddrPortFrom(netip.AddrFrom16([16]byte(a[:16])).Unmap(), binary.BigEndian.Uint16(a[16:18]))
	q.dst = netip.AddrPortFrom(netip.AddrFrom16([16]byte(a[18:34])).Unmap(), binary.BigEndian.Uint16(a[34:36]))
	return q, nil
}

func (m *Manager) handleStream(s Spec, p *pool, c *net.TCPConn) {
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	defer c.Close()
	up, err := m.upstream(s.Via, p)
	if err != nil {
		if s.Via.Session != "" {
			transport.Note(s.Via.Session, "a user connection on port %d found no session: %v", s.Port, err)
		}
		return
	}
	defer up.Close()
	q := request{port: s.Target.Port(), version: s.Version}
	if q.version != 0 {
		q.src = c.RemoteAddr().(*net.TCPAddr).AddrPort()
		q.dst = c.LocalAddr().(*net.TCPAddr).AddrPort()
	}
	if _, err := up.Write(q.marshal()); err != nil {
		if s.Via.Session != "" {
			transport.Note(s.Via.Session, "sending a user connection's request: %v", err)
		}
		return
	}
	pipe(c, up.(halfConn))
}

// upstream is a connection to the exit for one user: a ready one from the
// pool, or a stream on a connection tunnel's session.
func (m *Manager) upstream(v Via, p *pool) (net.Conn, error) {
	if v.Session != "" {
		h := m.hub.Load()
		if h == nil {
			return nil, errors.New("connection tunnels are not running")
		}
		return h.Open(v.Session)
	}
	return p.get()
}

// ---------- exit ----------

// exitServer holds the tunnels whose relay forwards end here.
type exitServer struct {
	exits   atomic.Pointer[map[string]Exit]
	waiting atomic.Int32
}

func newExitServer() *exitServer {
	x := &exitServer{}
	x.exits.Store(&map[string]Exit{})
	return x
}

// SyncExits serves the tunnels whose relay forwards end here.
func (m *Manager) SyncExits(exits []Exit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Tunnels of two main servers may share a name; their secrets differ.
	byName := map[string]Exit{}
	for _, e := range exits {
		k := e.Tunnel + "\x00" + e.Secret
		if cur, ok := byName[k]; ok {
			e.Ports = append(append([]uint16(nil), cur.Ports...), e.Ports...)
			e.Egress = e.Egress || cur.Egress
			e.From = append(append([]netip.Addr(nil), cur.From...), e.From...)
			e.Listen = append(append([]uint16(nil), cur.Listen...), e.Listen...)
			e.Public = append(append([]uint16(nil), cur.Public...), e.Public...)
			if !e.Addr.IsValid() {
				e.Addr = cur.Addr
			}
		}
		byName[k] = e
	}
	m.exit.exits.Store(&byName)
	return m.syncListen()
}

// SyncListen opens the listeners the exits and the listening connection
// tunnels need now, and closes the others.
func (m *Manager) SyncListen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncListen()
}

func (m *Manager) syncListen() error {
	want := map[uint16]bool{}
	for _, e := range *m.exit.exits.Load() {
		if e.Session {
			continue
		}
		if len(e.Listen) == 0 {
			want[uint16(m.exitPort)] = true
		}
		for _, p := range e.Listen {
			want[p] = true
		}
	}
	if h := m.hub.Load(); h != nil {
		for _, p := range h.ListenPorts() {
			want[p] = true
		}
	}
	var errs []error
	for p := range want {
		if m.listen[p] != nil {
			continue
		}
		ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: int(p)})
		if err != nil {
			errs = append(errs, fmt.Errorf("relay listener on TCP %d: %w", p, err))
			continue
		}
		m.listen[p] = ln
		go m.acceptExits(ln)
	}
	for p, ln := range m.listen {
		if !want[p] {
			ln.Close()
			delete(m.listen, p)
		}
	}
	return errors.Join(errs...)
}

// ListenPorts are the relay listeners open now.
func (m *Manager) ListenPorts() []uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint16, 0, len(m.listen))
	for p := range m.listen {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (m *Manager) acceptExits(ln *net.TCPListener) {
	x := m.exit
	for {
		c, err := ln.AcceptTCP()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if x.waiting.Add(1) > maxWaiting {
			x.waiting.Add(-1)
			c.Close()
			continue
		}
		go m.handleConn(x, c)
	}
}

// handleConn tells what came in by its first bytes: a relay connection, a
// connection tunnel's session (plain or TLS), or anything else, which gets
// the web server.
func (m *Manager) handleConn(x *exitServer, c *net.TCPConn) {
	waiting := true
	done := func() {
		if waiting {
			waiting = false
			x.waiting.Add(-1)
		}
	}
	defer done()
	c.SetDeadline(time.Now().Add(dialTimeout))
	var b [3]byte
	if _, err := io.ReadFull(c, b[:1]); err != nil {
		c.Close()
		return
	}
	h := m.hub.Load()
	if b[0] == 0x16 && h != nil { // a TLS handshake record
		done()
		h.ServeTLS(c, []byte{b[0]})
		return
	}
	if _, err := io.ReadFull(c, b[1:]); err != nil {
		c.Close()
		return
	}
	switch {
	case string(b[:]) == "NTR":
		m.handleExit(x, c, done)
	case string(b[:]) == "NTS" && h != nil:
		done()
		h.ServePlain(c)
	default:
		m.BadHandshakes.Add(1)
		transport.Decoy(c)
		c.Close()
	}
}

func (m *Manager) handleExit(x *exitServer, c *net.TCPConn, done func()) {
	defer c.Close()
	tcptun.Tune(c, notSentLowat)
	e, cipher, err := readPreamble(c, *x.exits.Load())
	if err != nil {
		m.BadHandshakes.Add(1)
		return
	}
	k, err := tcptun.NewStreamKeys(purpose(cipher), e.Secret, e.Tunnel, cipher, e.Side)
	if err != nil {
		return
	}
	sc, err := k.Accept(c)
	if err != nil {
		m.BadHandshakes.Add(1)
		return
	}
	// A pooled connection waits here for its user.
	c.SetReadDeadline(time.Now().Add(exitIdle))
	q, err := readRequest(sc)
	if err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	done()
	m.serveRequest(x, e, q, sc)
}

// serveRequest carries one user connection that arrived at the exit.
func (m *Manager) serveRequest(x *exitServer, e Exit, q request, sc net.Conn) {
	if q.host != "" {
		m.egress(x, e, q, sc)
		return
	}
	// The tunnel's forwards may have changed while the connection waited.
	e, ok := current(x, e)
	if !ok || !allowed(e.Ports, q.port) {
		return
	}
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	up, err := e.dialFor(q.port)
	if err != nil {
		return
	}
	defer up.Close()
	if q.version != 0 {
		if _, err := up.Write(Header(q.version, false, q.src, q.dst)); err != nil {
			return
		}
	}
	pipe(sc.(halfConn), up)
}

// serveSession carries a relay stream the far end opened on a connection
// tunnel's session (the hub's Handler).
func (m *Manager) serveSession(s transport.Spec, c net.Conn) {
	defer c.Close()
	x := m.exit
	e, ok := current(x, Exit{Tunnel: s.Tunnel, Secret: s.Secret})
	if !ok {
		transport.Note(s.Name, "a user connection came over the tunnel, but no forward here ends on it")
		return
	}
	c.SetReadDeadline(time.Now().Add(dialTimeout))
	q, err := readRequest(c)
	if err != nil {
		transport.Note(s.Name, "reading a user connection's request: %v", err)
		return
	}
	if q.host == "" && !allowed(e.Ports, q.port) {
		transport.Note(s.Name, "a user connection asked for port %d, which no forward here allows", q.port)
	}
	c.SetReadDeadline(time.Time{})
	m.serveRequest(x, e, q, c)
}

// readPreamble reads a relay connection's preamble after "NTR".
func readPreamble(c net.Conn, exits map[string]Exit) (Exit, string, error) {
	var h [2]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return Exit{}, "", err
	}
	if h[0] != streamVersion {
		return Exit{}, "", errors.New("not a relay connection")
	}
	name := make([]byte, h[1]+1) // the name, then the cipher's length
	if _, err := io.ReadFull(c, name); err != nil {
		return Exit{}, "", err
	}
	cipher := make([]byte, name[len(name)-1])
	if _, err := io.ReadFull(c, cipher); err != nil {
		return Exit{}, "", err
	}
	var from netip.Addr
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		from = a.AddrPort().Addr().Unmap()
	}
	e, ok := findExit(exits, string(name[:len(name)-1]), from)
	if !ok {
		return Exit{}, "", errors.New("unknown tunnel")
	}
	if err := tcptun.ValidCipher(string(cipher)); err != nil {
		return Exit{}, "", err
	}
	return e, string(cipher), nil
}

// findExit finds the tunnel a relay connection names. When tunnels of two
// main servers share the name, the connection's source tells them apart.
func findExit(exits map[string]Exit, name string, from netip.Addr) (Exit, bool) {
	var found []Exit
	for _, e := range exits {
		if e.Tunnel == name {
			found = append(found, e)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	for _, e := range found {
		if slices.Contains(e.From, from) {
			return e, true
		}
	}
	return Exit{}, false
}

// current is e as the exits are now (they may change while a connection
// waits in the pool).
func current(x *exitServer, e Exit) (Exit, bool) {
	cur, ok := (*x.exits.Load())[e.Tunnel+"\x00"+e.Secret]
	return cur, ok
}

func allowed(ports []uint16, p uint16) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// dialLocal connects to a service on this node, over IPv4 loopback or, for
// a service listening on IPv6 only, IPv6 loopback.
func dialLocal(port uint16) (*net.TCPConn, error) {
	var err error
	for _, a := range []string{"127.0.0.1", "::1"} {
		var c net.Conn
		c, err = net.DialTimeout("tcp", net.JoinHostPort(a, fmt.Sprint(port)), exitDialWait)
		if err == nil {
			return c.(*net.TCPConn), nil
		}
	}
	return nil, err
}
