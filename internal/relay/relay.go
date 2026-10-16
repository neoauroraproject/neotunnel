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
	"time"
)

// Spec is one port a proxy-mode forward listens on.
type Spec struct {
	Proto   string         // tcp or udp
	Port    uint16         // port on this node
	Target  netip.AddrPort // the far end's tunnel address and port
	Version int            // PROXY protocol version, 1 or 2
	Public  netip.Addr     // this node's public address (destination in UDP headers)
}

func (s Spec) key() string { return fmt.Sprintf("%s/%d", s.Proto, s.Port) }

const (
	dialTimeout = 10 * time.Second
	udpIdle     = 2 * time.Minute
)

// Manager runs the daemon's relays.
type Manager struct {
	mu  sync.Mutex
	run map[string]*server

	Conns atomic.Int64 // open TCP connections and UDP sessions
}

func NewManager() *Manager { return &Manager{run: map[string]*server{}} }

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
		if s.Proto == "udp" {
			srv, err = m.serveUDP(s)
		} else {
			srv, err = m.serveTCP(s)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("PROXY protocol relay on %s port %d: %w", s.Proto, s.Port, err))
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

func (m *Manager) Close() { m.Sync(nil) }

func (m *Manager) serveTCP(s Spec) (*server, error) {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: int(s.Port)})
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
			go m.handleTCP(s, c)
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

// pipe copies both ways (spliced in the kernel on Linux) and passes on
// half-closes so protocols that rely on them keep working.
func pipe(a, b *net.TCPConn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src *net.TCPConn) {
		io.Copy(dst, src)
		dst.CloseWrite()
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
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
				go func(client netip.AddrPort) {
					defer m.Conns.Add(-1)
					rb := make([]byte, 65535)
					for {
						n, err := up.Read(rb)
						if err != nil {
							return
						}
						ss.last.Store(time.Now().UnixNano())
						pc.WriteToUDPAddrPort(rb[:n], client)
					}
				}(from)
			}
			mu.Unlock()
			ss.last.Store(time.Now().UnixNano())
			hdr := Header(2, true, from, dst)
			ss.conn.Write(append(hdr, buf[:n]...))
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
