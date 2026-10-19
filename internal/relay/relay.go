package relay

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Spec is one port a proxy-mode forward listens on.
type Spec struct {
	Proto   string         // tcp or udp
	Port    uint16         // port on this node
	Target  netip.AddrPort // the far end's tunnel address and port
	Version int            // PROXY protocol version, 1 or 2
	Public  netip.Addr     // this node's public address (destination in UDP headers)
	// Via, when set, makes this a relay-mode forward (TCP only): the user's
	// connection goes to the far node as its own TCP connection, which
	// connects to Target's port on its loopback. Version 0 sends no header.
	Via Via
	// Socks makes it a SOCKS5 and HTTP proxy on Listen:Port (see socks.go):
	// each connection goes to Via's far node, which connects to the
	// address the program asked for. Auth is "user:password" or "".
	Socks  bool
	Listen netip.Addr
	Auth   string
	// FetchSites makes it the downloads listener (see fetch.go): connections
	// redirected here go to Via's far node when they name one of these
	// sites (comma-separated), directly otherwise. FetchStrict never sends
	// a listed site directly.
	FetchSites  string
	FetchStrict bool
}

func (s Spec) key() string { return fmt.Sprintf("%s/%d", s.Proto, s.Port) }

const (
	dialTimeout = 10 * time.Second
	udpIdle     = 2 * time.Minute
)

// Manager runs the daemon's relays.
type Manager struct {
	mu       sync.Mutex
	run      map[string]*server
	pools    map[Via]*pool
	exit     *exitServer
	exitPort int

	Conns         atomic.Int64  // open TCP connections and UDP sessions
	BadHandshakes atomic.Uint64 // relay connections that failed to authenticate
}

func NewManager() *Manager { return newManager(RelayPort) }

func newManager(exitPort int) *Manager {
	return &Manager{run: map[string]*server{}, pools: map[Via]*pool{}, exitPort: exitPort}
}

type server struct {
	spec  Spec
	close func()
}

// Sync starts and stops listeners to match specs. A changed target only
// affects new connections.
func (m *Manager) Sync(specs []Spec) []error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	// Relay-mode forwards share one pool of ready connections per far end.
	vias := map[Via]bool{}
	for _, s := range specs {
		if s.Via.Tunnel != "" {
			vias[s.Via] = true
		}
	}
	for v, p := range m.pools {
		if !vias[v] {
			p.close()
			delete(m.pools, v)
		}
	}
	want := map[string]bool{}
	for _, s := range specs {
		k := s.key()
		want[k] = true
		if cur, ok := m.run[k]; ok {
			if cur.spec == s {
				continue
			}
			cur.close()
			delete(m.run, k)
		}
		var srv *server
		var err error
		switch {
		case s.FetchSites != "":
			srv, err = m.serveFetch(s)
		case s.Socks:
			srv, err = m.serveSocks(s)
		case s.Via.Tunnel != "" && s.Proto == "tcp":
			srv, err = m.serveStream(s)
		case s.Via.Tunnel != "":
			err = errors.New("relay mode carries TCP only")
		case s.Proto == "udp":
			srv, err = m.serveUDP(s)
		default:
			srv, err = m.serveTCP(s)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("relay on %s port %d: %w", s.Proto, s.Port, err))
			continue
		}
		m.run[k] = srv
	}
	for k, srv := range m.run {
		if !want[k] {
			srv.close()
			delete(m.run, k)
		}
	}
	return errs
}

func (m *Manager) Close() {
	m.Sync(nil)
	m.SyncExits(nil)
}

func (m *Manager) serveTCP(s Spec) (*server, error) {
	return m.listenTCP(s, func(c *net.TCPConn) { m.handleTCP(s, c) })
}

// serveStream serves a relay-mode forward (see stream.go).
func (m *Manager) serveStream(s Spec) (*server, error) {
	p := m.pools[s.Via]
	if p == nil {
		var err error
		if p, err = newPool(s.Via); err != nil {
			return nil, err
		}
		m.pools[s.Via] = p
	}
	return m.listenTCP(s, func(c *net.TCPConn) { m.handleStream(s, p, c) })
}

func (m *Manager) listenTCP(s Spec, handle func(*net.TCPConn)) (*server, error) {
	laddr := &net.TCPAddr{Port: int(s.Port)}
	if s.Listen.IsValid() {
		laddr.IP = s.Listen.AsSlice()
	}
	ln, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.AcceptTCP()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Printf("relay tcp/%d: %v", s.Port, err)
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return
			}
			go handle(c)
		}
	}()
	return &server{spec: s, close: func() { ln.Close() }}, nil
}

func (m *Manager) handleTCP(s Spec, c *net.TCPConn) {
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	defer c.Close()
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	up, err := d.Dial("tcp", s.Target.String())
	if err != nil {
		return
	}
	defer up.Close()
	src := c.RemoteAddr().(*net.TCPAddr).AddrPort()
	dst := c.LocalAddr().(*net.TCPAddr).AddrPort()
	if _, err := up.Write(Header(s.Version, false, src, dst)); err != nil {
		return
	}
	pipe(c, up.(*net.TCPConn))
}

// halfConn is a connection that can end its sending side alone.
type halfConn interface {
	net.Conn
	CloseWrite() error
}

// pipe copies both ways (spliced in the kernel on Linux) and passes on
// half-closes so protocols that rely on them keep working. A direction that
// fails (reset, dead client) ends both at once, so a broken connection never
// lingers holding the other side open.
func pipe(a, b halfConn) {
	done := make(chan error, 2)
	cp := func(dst, src halfConn) {
		_, err := io.Copy(dst, src)
		dst.CloseWrite()
		done <- err
	}
	go cp(a, b)
	go cp(b, a)
	if err := <-done; err != nil {
		a.Close()
		b.Close()
	}
	<-done
}

type udpSession struct {
	conn *net.UDPConn
	last atomic.Int64
}

func (m *Manager) serveUDP(s Spec) (*server, error) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{Port: int(s.Port)})
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	sessions := map[netip.AddrPort]*udpSession{}
	stop := make(chan struct{})
	dst := netip.AddrPortFrom(s.Public, s.Port)

	// Idle sessions are closed, which also ends their reply readers.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			mu.Lock()
			for k, ss := range sessions {
				if time.Since(time.Unix(0, ss.last.Load())) > udpIdle {
					ss.conn.Close()
					delete(sessions, k)
				}
			}
			mu.Unlock()
		}
	}()

	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFromUDPAddrPort(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			mu.Lock()
			ss := sessions[from]
			if ss == nil {
				up, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(s.Target))
				if err != nil {
					mu.Unlock()
					continue
				}
				ss = &udpSession{conn: up}
				sessions[from] = ss
				m.Conns.Add(1)
				go func(client netip.AddrPort, ss *udpSession) {
					defer m.Conns.Add(-1)
					// A session whose socket broke is dropped, so the
					// client's next packet starts a fresh one.
					defer func() {
						mu.Lock()
						if sessions[client] == ss {
							delete(sessions, client)
						}
						mu.Unlock()
						up.Close()
					}()
					rb := make([]byte, 65535)
					for {
						n, err := up.Read(rb)
						if errors.Is(err, syscall.ECONNREFUSED) {
							continue // the service was briefly down; keep going
						}
						if err != nil {
							return
						}
						ss.last.Store(time.Now().UnixNano())
						pc.WriteToUDPAddrPort(rb[:n], client)
					}
				}(from, ss)
			}
			mu.Unlock()
			ss.last.Store(time.Now().UnixNano())
			hdr := Header(2, true, from, dst)
			if _, err := ss.conn.Write(append(hdr, buf[:n]...)); err != nil && !errors.Is(err, syscall.ECONNREFUSED) {
				ss.conn.Close() // its reader ends and drops the session
			}
		}
	}()
	return &server{spec: s, close: func() {
		close(stop)
		pc.Close()
		mu.Lock()
		for _, ss := range sessions {
			ss.conn.Close()
		}
		mu.Unlock()
	}}, nil
}
