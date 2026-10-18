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

	"neotunnel/internal/mmsg"
	"neotunnel/internal/offload"
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

const (
	batch   = 64  // packets per receive
	maxSegs = 128 // frames one super-frame is cut into, at most
	// maxPacket is the largest packet received: a jumbo frame (the
	// largest MTU is 9000), its EoIP header and an outer IP header.
	maxPacket = 9216 + 64
	// headroom is left in front of every received packet, so the frame in
	// it has room for a virtio-net header (see package offload).
	headroom = 16
)

// carrier moves EoIP packets (header and frame, no outer IP) to the far end.
type carrier interface {
	// readBatch receives up to len(bufs) packets and sets pkts[i] to the
	// EoIP packet in bufs[i], or nil for one to ignore.
	readBatch(bufs, pkts [][]byte) (int, error)
	writeBatch(ps [][]byte) error
	write(p []byte) error
	Close() error
}

// rawCarrier is EoIP over IP protocol 47 / EoIPv6 over 97. The socket is
// connected, so the kernel only hands it packets from the far end.
type rawCarrier struct {
	conn  *net.IPConn
	v4    bool
	r, w  *mmsg.Conn
	sizes []int
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
	rd, err1 := mmsg.New(conn, batch)
	wr, err2 := mmsg.New(conn, maxSegs)
	if err := errors.Join(err1, err2); err != nil {
		conn.Close()
		return nil, err
	}
	return &rawCarrier{conn: conn, v4: c.Remote.Is4(), r: rd, w: wr, sizes: make([]int, batch)}, nil
}

func (r *rawCarrier) readBatch(bufs, pkts [][]byte) (int, error) {
	n, err := r.r.Read(bufs, r.sizes, nil)
	for i := range n {
		pkts[i] = nil
		if r.sizes[i] < 0 {
			continue
		}
		p := bufs[i][:r.sizes[i]]
		// Raw IPv4 sockets deliver the IP header too; IPv6 ones never do.
		if r.v4 && len(p) > 0 && p[0]>>4 == 4 {
			ihl := int(p[0]&0x0f) * 4
			if ihl > len(p) {
				continue
			}
			p = p[ihl:]
		}
		pkts[i] = p
	}
	return n, err
}

// The socket is connected: no destination.
func (r *rawCarrier) writeBatch(ps [][]byte) error { return r.w.Write(ps, netip.AddrPort{}, !r.v4) }
func (r *rawCarrier) write(p []byte) error         { _, err := r.conn.Write(p); return err }
func (r *rawCarrier) Close() error                 { return r.conn.Close() }

// udpCarrier is EoIP inside UDP datagrams on a chosen port.
type udpCarrier struct {
	conn   *net.UDPConn
	remote netip.AddrPort
	r, w   *mmsg.Conn
	sizes  []int
	from   []netip.AddrPort
	noGSO  bool // UDP GSO failed once (writers only)
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
	rd, err1 := mmsg.New(conn, batch)
	wr, err2 := mmsg.New(conn, maxSegs)
	if err := errors.Join(err1, err2); err != nil {
		conn.Close()
		return nil, err
	}
	return &udpCarrier{conn: conn, remote: netip.AddrPortFrom(c.Remote, c.Port), r: rd, w: wr,
		sizes: make([]int, batch), from: make([]netip.AddrPort, batch)}, nil
}

func (u *udpCarrier) readBatch(bufs, pkts [][]byte) (int, error) {
	n, err := u.r.Read(bufs, u.sizes, u.from)
	for i := range n {
		pkts[i] = nil
		// EoIP has no authentication: only the far end's address may talk.
		if u.sizes[i] >= 0 && u.from[i].Addr() == u.remote.Addr() {
			pkts[i] = bufs[i][:u.sizes[i]]
		}
	}
	return n, err
}

// writeBatch sends with UDP GSO when the packets are the same size (the
// pieces of one super-frame), else with one sendmmsg.
func (u *udpCarrier) writeBatch(ps [][]byte) error {
	v6 := u.remote.Addr().Is6()
	if len(ps) > 1 && !u.noGSO && sameSize(ps) {
		if err := u.w.WriteGSO(ps, u.remote, v6); err == nil {
			return nil
		}
		u.noGSO = true
	}
	return u.w.Write(ps, u.remote, v6)
}

// sameSize: all of b but the last are as long as the first, the last no
// longer.
func sameSize(b [][]byte) bool {
	n := len(b[0])
	for _, d := range b[1 : len(b)-1] {
		if len(d) != n {
			return false
		}
	}
	return len(b[len(b)-1]) <= n
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
	vnet bool // tap has a virtio-net header and offload
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
	tap, vnet, err := openTAP(c.Name, c.mac())
	if err != nil {
		car.Close()
		return nil, err
	}
	l := &Link{cfg: c, fr: c.framing(), tap: tap, vnet: vnet, car: car, done: make(chan struct{})}
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

// outbound: TAP -> EoIP. Frames are read in behind room for the header. A
// TCP super-frame is cut into ordinary frames, each with its own EoIP
// header, and they all go out in one system call.
func (l *Link) outbound() {
	defer l.wg.Done()
	hl := l.fr.headerLen()
	at := max(hl, offload.HdrLen) // where the frame starts in buf
	start := at
	if l.vnet {
		start -= offload.HdrLen
	}
	buf := make([]byte, at+65535)
	segs, sizes, out := make([][]byte, maxSegs), make([]int, maxSegs), make([][]byte, maxSegs)
	for {
		n, err := l.tap.Read(buf[start:])
		if err != nil {
			if !l.closed() && !errors.Is(err, os.ErrClosed) {
				log.Printf("eoip %s: read: %v", l.cfg.Name, err)
			}
			return
		}
		frame := buf[at : start+n]
		h := offload.Hdr{}
		if l.vnet {
			h = offload.ParseHdr(buf[start:])
		}
		if h.GSOType == offload.GSONone {
			if offload.Complete(frame, h) != nil {
				continue
			}
			l.send(buf[at-hl:], len(frame))
			continue
		}
		nseg, size := offload.Segments(frame, h)
		if nseg == 0 || nseg > maxSegs {
			continue
		}
		for i := range segs[:nseg] {
			if len(segs[i]) < hl+size {
				segs[i] = make([]byte, hl+size)
			}
		}
		k, err := offload.Segment(frame, h, offload.L3(frame), segs, hl, sizes)
		if err != nil {
			continue
		}
		for i := range k {
			l.fr.put(segs[i], l.cfg.ID, sizes[i])
			out[i] = segs[i][:hl+sizes[i]]
		}
		if l.car.writeBatch(out[:k]) == nil {
			l.sent.Store(time.Now().UnixNano())
		}
	}
}

// inbound: EoIP -> TAP, for packets with this tunnel's id, a batch per
// system call; consecutive TCP segments of a flow go to the interface as
// one super-frame.
func (l *Link) inbound() {
	defer l.wg.Done()
	full, bufs, pkts := make([][]byte, batch), make([][]byte, batch), make([][]byte, batch)
	for i := range full {
		full[i] = make([]byte, headroom+maxPacket)
		bufs[i] = full[i][headroom:]
	}
	// Merged segments are not checked again by the kernel, and EoIP has no
	// authentication: check each one.
	gro := offload.Coalescer{L3: 14, Verify: true}
	write := func(b []byte) error { l.tap.Write(b); return nil }
	for {
		k, err := l.car.readBatch(bufs, pkts)
		if err != nil {
			if !l.closed() {
				log.Printf("eoip %s: receive: %v", l.cfg.Name, err)
			}
			return
		}
		for i := range k {
			if pkts[i] == nil {
				l.BadPackets.Add(1)
				continue
			}
			id, frame, err := l.fr.parse(pkts[i])
			if err != nil || id != l.cfg.ID {
				// Other GRE traffic (kernel tunnels, other EoIP ids) shares the socket.
				l.BadPackets.Add(1)
				continue
			}
			if len(frame) == 0 {
				continue
			}
			if !l.vnet {
				l.tap.Write(frame)
				continue
			}
			off := cap(full[i]) - cap(frame) // where frame starts in full[i]
			gro.Add(full[i][off-offload.HdrLen : off+len(frame)])
		}
		if l.vnet {
			gro.Flush(write)
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
