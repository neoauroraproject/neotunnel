package relay

// Relay mode carries a TCP forward without a packet tunnel: this node (the
// entry) accepts the user's connection and opens a TCP connection of its own
// to the far node (the exit), which connects onward to the service there.
// Every user connection is then an ordinary TCP connection on each leg, with
// its own congestion control, instead of TCP inside a tunnel's TCP stream
// or packets: on a long, lossy path one user's losses slow only that user.
//
// The exit listens on RelayPort. Each connection starts with a short plain
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
// then the user's bytes both ways. The exit only connects to its own
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
	"sync"
	"sync/atomic"
	"time"

	"neotunnel/internal/tcptun"
)

// RelayPort is where every node accepts relay connections from its peers.
const RelayPort = 7712

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
	Cipher string // "" = authenticated, not encrypted
	Pool   int    // connections kept ready
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

	mu     sync.Mutex
	idle   []*pooled
	inside atomic.Bool // the public listener failed; use the tunnel's
	wake   chan struct{}
	stop   chan struct{}
	once   sync.Once
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
	// The way that worked last goes first; the other is the fallback.
	ways := []netip.AddrPort{p.via.Exit, p.via.Inside}
	if p.inside.Load() {
		ways[0], ways[1] = ways[1], ways[0]
	}
	var c net.Conn
	var err error
	for i, to := range ways {
		if !to.IsValid() {
			continue
		}
		d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
		if i == 0 && ways[1].IsValid() {
			d.Timeout = 3 * time.Second
		}
		if c, err = d.Dial("tcp", to.String()); err == nil {
			p.inside.Store(to == p.via.Inside)
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
	up, err := p.get()
	if err != nil {
		return
	}
	defer up.Close()
	q := request{port: s.Target.Port(), version: s.Version}
	if q.version != 0 {
		q.src = c.RemoteAddr().(*net.TCPAddr).AddrPort()
		q.dst = c.LocalAddr().(*net.TCPAddr).AddrPort()
	}
	if _, err := up.Write(q.marshal()); err != nil {
		return
	}
	pipe(c, up.(halfConn))
}

// ---------- exit ----------

type exitServer struct {
	port    int
	ln      net.Listener
	exits   atomic.Pointer[map[string]Exit]
	waiting atomic.Int32
}

// SyncExits serves the tunnels whose relay forwards end here (none: stop).
func (m *Manager) SyncExits(exits []Exit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(exits) == 0 {
		if m.exit != nil {
			m.exit.ln.Close()
			m.exit = nil
		}
		return nil
	}
	byName := map[string]Exit{}
	for _, e := range exits {
		if cur, ok := byName[e.Tunnel]; ok {
			e.Ports = append(append([]uint16(nil), cur.Ports...), e.Ports...)
			e.Egress = e.Egress || cur.Egress
		}
		byName[e.Tunnel] = e
	}
	if m.exit == nil {
		ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: m.exitPort})
		if err != nil {
			return fmt.Errorf("relay listener on TCP %d: %w", m.exitPort, err)
		}
		x := &exitServer{port: m.exitPort, ln: ln}
		x.exits.Store(&byName) // before the first connection is accepted
		m.exit = x
		go m.acceptExits(x, ln)
	}
	m.exit.exits.Store(&byName)
	return nil
}

func (m *Manager) acceptExits(x *exitServer, ln *net.TCPListener) {
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
		go m.handleExit(x, c)
	}
}

func (m *Manager) handleExit(x *exitServer, c *net.TCPConn) {
	waiting := true
	done := func() {
		if waiting {
			waiting = false
			x.waiting.Add(-1)
		}
	}
	defer done()
	defer c.Close()
	tcptun.Tune(c, notSentLowat)
	c.SetDeadline(time.Now().Add(dialTimeout))
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
	c.SetReadDeadline(time.Time{})
	done()
	if q.host != "" {
		m.egress(x, e.Tunnel, q, sc)
		return
	}
	// The tunnel's forwards may have changed while the connection waited.
	if e, ok := (*x.exits.Load())[e.Tunnel]; !ok || !allowed(e.Ports, q.port) {
		return
	}
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	up, err := dialLocal(q.port)
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

func readPreamble(c net.Conn, exits map[string]Exit) (Exit, string, error) {
	var h [5]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return Exit{}, "", err
	}
	if string(h[:3]) != "NTR" || h[3] != streamVersion {
		return Exit{}, "", errors.New("not a relay connection")
	}
	name := make([]byte, h[4]+1) // the name, then the cipher's length
	if _, err := io.ReadFull(c, name); err != nil {
		return Exit{}, "", err
	}
	cipher := make([]byte, name[len(name)-1])
	if _, err := io.ReadFull(c, cipher); err != nil {
		return Exit{}, "", err
	}
	e, ok := exits[string(name[:len(name)-1])]
	if !ok {
		return Exit{}, "", errors.New("unknown tunnel")
	}
	if err := tcptun.ValidCipher(string(cipher)); err != nil {
		return Exit{}, "", err
	}
	return e, string(cipher), nil
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
