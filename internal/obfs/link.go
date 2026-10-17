package obfs

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/tun"
)

// Config describes this node's end of a NeoObfs tunnel.
type Config struct {
	Name      string // interface name
	Tunnel    string // tunnel name (keys are bound to it)
	Secret    string
	Side      int
	Remote    netip.AddrPort // where the far end listens
	Port      uint16         // where this end listens
	Padding   int
	Keepalive time.Duration // 0 = off
}

// Link is a running tunnel end: a TUN interface and a UDP socket.
type Link struct {
	cfg   Config
	tun   *os.File
	conn  *net.UDPConn
	codec *codec
	peer  atomic.Pointer[netip.AddrPort]
	sent  atomic.Int64 // unix nanos of the last datagram sent
	done  chan struct{}
	wg    sync.WaitGroup

	BadDatagrams atomic.Uint64
}

func keys(c Config) (send, recv []byte, err error) {
	if send, err = config.DeriveKey(c.Secret, c.Tunnel, fmt.Sprintf("obfs %d>%d", c.Side, 1-c.Side), 32); err != nil {
		return
	}
	recv, err = config.DeriveKey(c.Secret, c.Tunnel, fmt.Sprintf("obfs %d>%d", 1-c.Side, c.Side), 32)
	return
}

// Start opens the interface and socket and starts moving packets.
func Start(c Config) (*Link, error) {
	send, recv, err := keys(c)
	if err != nil {
		return nil, err
	}
	cd, err := newCodec(send, recv, c.Padding)
	if err != nil {
		return nil, err
	}
	network := "udp4"
	if c.Remote.Addr().Is6() {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, &net.UDPAddr{Port: int(c.Port)})
	if err != nil {
		return nil, fmt.Errorf("listen on UDP %d: %w", c.Port, err)
	}
	conn.SetReadBuffer(4 << 20)
	conn.SetWriteBuffer(4 << 20)
	tf, err := tun.Open(c.Name)
	if err != nil {
		conn.Close()
		return nil, err
	}
	l := &Link{cfg: c, tun: tf, conn: conn, codec: cd, done: make(chan struct{})}
	remote := c.Remote
	l.peer.Store(&remote)
	l.wg.Add(2)
	go l.outbound()
	go l.inbound()
	if c.Keepalive > 0 {
		l.wg.Add(1)
		go l.keepalive()
	}
	return l, nil
}

// Close stops the link; its interface disappears.
func (l *Link) Close() {
	select {
	case <-l.done:
		return
	default:
	}
	close(l.done)
	l.tun.Close()
	l.conn.Close()
	l.wg.Wait()
}

func (l *Link) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *Link) send(typ byte, pkt []byte, buf []byte) {
	d := l.codec.seal(buf, typ, pkt, uint32(time.Now().Unix()))
	if _, err := l.conn.WriteToUDPAddrPort(d, *l.peer.Load()); err == nil {
		l.sent.Store(time.Now().UnixNano())
	}
}

// outbound: TUN -> encrypt -> UDP. Packets are read straight into the
// datagram buffer behind room for the nonce and header, so sealing is in place.
func (l *Link) outbound() {
	defer l.wg.Done()
	buf := make([]byte, l.codec.room(65535))
	off := nonceLen + headerLen
	for {
		n, err := l.tun.Read(buf[off : len(buf)-tagLen-l.codec.padding])
		if err != nil {
			if !l.closed() && !errors.Is(err, os.ErrClosed) {
				log.Printf("obfs %s: read: %v", l.cfg.Name, err)
			}
			return
		}
		l.send(typeData, buf[off:off+n], buf)
	}
}

// inbound: UDP -> authenticate, decrypt -> TUN. Any address that proves it
// holds the key becomes the far end, so a NAT rebinding doesn't break us.
func (l *Link) inbound() {
	defer l.wg.Done()
	buf := make([]byte, 65535+Overhead+255)
	for {
		n, from, err := l.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if !l.closed() {
				log.Printf("obfs %s: receive: %v", l.cfg.Name, err)
			}
			return
		}
		typ, pkt, err := l.codec.open(buf[:n])
		if err != nil {
			l.BadDatagrams.Add(1)
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		if p := l.peer.Load(); *p != from {
			l.peer.Store(&from)
		}
		if typ == typeData && len(pkt) > 0 {
			l.tun.Write(pkt)
		}
	}
}

// keepalive keeps NAT mappings open and lets the far end learn our address.
func (l *Link) keepalive() {
	defer l.wg.Done()
	buf := make([]byte, l.codec.room(0))
	l.send(typeKeepalive, nil, buf)
	t := time.NewTicker(l.cfg.Keepalive / 2)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, l.sent.Load())) >= l.cfg.Keepalive {
				l.send(typeKeepalive, nil, buf)
			}
		}
	}
}

// Manager runs the NeoObfs links of the daemon.
type Manager struct {
	mu    sync.Mutex
	links map[string]*Link
}

func NewManager() *Manager { return &Manager{links: map[string]*Link{}} }

// Sync starts, restarts (when changed) and stops links to match cfgs.
func (m *Manager) Sync(cfgs []Config) []error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	want := map[string]bool{}
	for _, c := range cfgs {
		want[c.Name] = true
		if cur, ok := m.links[c.Name]; ok {
			if cur.cfg == c {
				continue
			}
			cur.Close()
			delete(m.links, c.Name)
		}
		l, err := Start(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("tunnel %s: %w", c.Tunnel, err))
			continue
		}
		m.links[c.Name] = l
	}
	for name, l := range m.links {
		if !want[name] {
			l.Close()
			delete(m.links, name)
		}
	}
	return errs
}

// Close stops every link.
func (m *Manager) Close() { m.Sync(nil) }
