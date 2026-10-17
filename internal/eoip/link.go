package eoip

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Config describes this node's end of an EoIP tunnel.
type Config struct {
	Name      string // interface name
	Tunnel    string // tunnel name (the interface's MAC is derived from it)
	Side      int
	ID        uint16 // tunnel id, the same on both ends
	Local     netip.Addr
	Remote    netip.Addr
	Port      uint16 // 0 = raw IP (protocol 47, or 97 for EoIPv6); else UDP
	TTL       uint8
	DSCP      uint8
	NoDF      bool
	Keepalive time.Duration // 0 = off
}

func (c Config) framing() framing {
	if c.Port == 0 && c.Remote.Is6() {
		return framingEtherIP
	}
	return framingGRE
}

// mac is a stable, locally administered address for this end, so the far
// end's neighbor caches survive a daemon restart.
func (c Config) mac() net.HardwareAddr {
	h := sha256.Sum256([]byte(fmt.Sprintf("neotunnel/eoip/%s/%d", c.Tunnel, c.Side)))
	m := net.HardwareAddr(h[:6])
	m[0] = m[0]&^0x01 | 0x02
	return m
}

// carrier moves EoIP packets (header and frame, no outer IP) to the far end.
type carrier interface {
	read(b []byte) ([]byte, error)
	write(p []byte) error
	Close() error
}

// rawCarrier is EoIP over IP protocol 47 / EoIPv6 over 97. The socket is
// connected, so the kernel only hands it packets from the far end.
type rawCarrier struct {
	conn *net.IPConn
	v4   bool
}

func dialRaw(c Config) (*rawCarrier, error) {
	network := "ip4:47"
	if c.Remote.Is6() {
		network = "ip6:97"
	}
	var laddr *net.IPAddr
	if c.Local.IsValid() {
		laddr = &net.IPAddr{IP: c.Local.AsSlice()}
	}
	conn, err := net.DialIP(network, laddr, &net.IPAddr{IP: c.Remote.AsSlice()})
	if err != nil {
		return nil, fmt.Errorf("open raw %s socket: %w", network, err)
	}
	return &rawCarrier{conn: conn, v4: c.Remote.Is4()}, nil
}

func (r *rawCarrier) read(b []byte) ([]byte, error) {
	n, err := r.conn.Read(b)
	if err != nil {
		return nil, err
	}
	p := b[:n]
	// Raw IPv4 sockets deliver the IP header too; IPv6 ones never do.
	if r.v4 && len(p) > 0 && p[0]>>4 == 4 {
		ihl := int(p[0]&0x0f) * 4
		if ihl > len(p) {
			return nil, errPacket
		}
		p = p[ihl:]
	}
	return p, nil
}

func (r *rawCarrier) write(p []byte) error { _, err := r.conn.Write(p); return err }
func (r *rawCarrier) Close() error         { return r.conn.Close() }

// udpCarrier is EoIP inside UDP datagrams on a chosen port.
type udpCarrier struct {
	conn   *net.UDPConn
	remote netip.AddrPort
}

func listenUDP(c Config) (*udpCarrier, error) {
	network := "udp4"
	if c.Remote.Is6() {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, &net.UDPAddr{Port: int(c.Port)})
	if err != nil {
		return nil, fmt.Errorf("listen on UDP %d: %w", c.Port, err)
	}
	return &udpCarrier{conn: conn, remote: netip.AddrPortFrom(c.Remote, c.Port)}, nil
}

func (u *udpCarrier) read(b []byte) ([]byte, error) {
	for {
		n, from, err := u.conn.ReadFromUDPAddrPort(b)
		if err != nil {
			return nil, err
		}
		// EoIP has no authentication: only the far end's address may talk.
		if from.Addr().Unmap() == u.remote.Addr() {
			return b[:n], nil
		}
	}
}

func (u *udpCarrier) write(p []byte) error {
	_, err := u.conn.WriteToUDPAddrPort(p, u.remote)
	return err
}

func (u *udpCarrier) Close() error { return u.conn.Close() }

// Link is a running tunnel end: a TAP interface and a socket.
type Link struct {
	cfg  Config
	fr   framing
	tap  *os.File
	car  carrier
	sent atomic.Int64 // unix nanos of the last packet sent
	done chan struct{}
	wg   sync.WaitGroup

	BadPackets atomic.Uint64
}

// Start opens the interface and socket and starts moving frames.
func Start(c Config) (*Link, error) {
	if c.Port == 0 && c.Remote.Is6() && c.ID > maxEtherIPID {
		return nil, fmt.Errorf("EoIPv6 tunnel id %d is over %d", c.ID, maxEtherIPID)
	}
	var (
		car carrier
		err error
	)
	if c.Port == 0 {
		car, err = dialRaw(c)
	} else {
		car, err = listenUDP(c)
	}
	if err != nil {
		return nil, err
	}
	if err := tune(car, c); err != nil {
		car.Close()
		return nil, err
	}
	tap, err := openTAP(c.Name, c.mac())
	if err != nil {
		car.Close()
		return nil, err
	}
	l := &Link{cfg: c, fr: c.framing(), tap: tap, car: car, done: make(chan struct{})}
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
	l.tap.Close()
	l.car.Close()
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

// send frames b[hl:hl+n] (b[:hl] is room for the header) and sends it.
func (l *Link) send(b []byte, n int) {
	hl := l.fr.headerLen()
	l.fr.put(b, l.cfg.ID, n)
	// A frame over the path MTU with DF set fails here, like it would be
	// dropped on the path; the far end never sees a partial frame.
	if l.car.write(b[:hl+n]) == nil {
		l.sent.Store(time.Now().UnixNano())
	}
}

// outbound: TAP -> EoIP. Frames are read in behind room for the header.
func (l *Link) outbound() {
	defer l.wg.Done()
	hl := l.fr.headerLen()
	buf := make([]byte, hl+65535)
	for {
		n, err := l.tap.Read(buf[hl:])
		if err != nil {
			if !l.closed() && !errors.Is(err, os.ErrClosed) {
				log.Printf("eoip %s: read: %v", l.cfg.Name, err)
			}
			return
		}
		l.send(buf, n)
	}
}

// inbound: EoIP -> TAP, for packets with this tunnel's id.
func (l *Link) inbound() {
	defer l.wg.Done()
	buf := make([]byte, 65535+GREHeaderLen)
	for {
		p, err := l.car.read(buf)
		if err != nil {
			if l.closed() {
				return
			}
			if errors.Is(err, errPacket) {
				l.BadPackets.Add(1)
				continue
			}
			log.Printf("eoip %s: receive: %v", l.cfg.Name, err)
			return
		}
		id, frame, err := l.fr.parse(p)
		if err != nil || id != l.cfg.ID {
			// Other GRE traffic (kernel tunnels, other EoIP ids) shares the socket.
			l.BadPackets.Add(1)
			continue
		}
		if len(frame) > 0 {
			l.tap.Write(frame)
		}
	}
}

// keepalive sends empty EoIP packets like MikroTik does, which also keeps
// NAT mappings open on UDP.
func (l *Link) keepalive() {
	defer l.wg.Done()
	buf := make([]byte, GREHeaderLen)
	l.send(buf, 0)
	t := time.NewTicker(l.cfg.Keepalive / 2)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, l.sent.Load())) >= l.cfg.Keepalive {
				l.send(buf, 0)
			}
		}
	}
}

// Manager runs the EoIP links of the daemon.
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
