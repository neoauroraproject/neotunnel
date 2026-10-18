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
	"neotunnel/internal/mmsg"
	"neotunnel/internal/offload"
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

const (
	batch   = 64  // datagrams per receive
	maxSegs = 128 // packets one super-packet is cut into, at most
	// maxDatagram is the largest datagram received (the largest inner MTU
	// is 9000).
	maxDatagram = 9216 + Overhead + 255
)

// openTUN opens the interface with a virtio-net header and offload, so it
// hands out and takes TCP super-packets (see package offload); plain when
// the kernel can't.
func openTUN(name string) (*os.File, bool, error) {
	if f, err := tun.OpenVnet(name); err == nil {
		if tun.SetOffload(f, true) == nil {
			return f, true, nil
		}
		f.Close()
	}
	f, err := tun.Open(name)
	return f, false, err
}

// Link is a running tunnel end: a TUN interface and a UDP socket.
type Link struct {
	cfg   Config
	tun   *os.File
	vnet  bool // tun has a virtio-net header and offload
	conn  *net.UDPConn
	r, w  *mmsg.Conn // batched receives and sends on conn
	noGSO bool       // UDP GSO failed once (outbound only)
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
	r, err1 := mmsg.New(conn, batch)
	w, err2 := mmsg.New(conn, maxSegs)
	if err := errors.Join(err1, err2); err != nil {
		conn.Close()
		return nil, err
	}
	tf, vnet, err := openTUN(c.Name)
	if err != nil {
		conn.Close()
		return nil, err
	}
	l := &Link{cfg: c, tun: tf, vnet: vnet, conn: conn, r: r, w: w, codec: cd, done: make(chan struct{})}
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
// datagram buffer behind room for the nonce and header, so sealing is in
// place. A TCP super-packet is cut into packets, each sealed into its own
// datagram, and they all go out in one system call.
func (l *Link) outbound() {
	defer l.wg.Done()
	buf := make([]byte, l.codec.room(65535))
	off := nonceLen + headerLen
	start := off
	if l.vnet {
		start -= offload.HdrLen
	}
	segs, sizes, out := make([][]byte, maxSegs), make([]int, maxSegs), make([][]byte, maxSegs)
	v6 := l.cfg.Remote.Addr().Is6()
	for {
		n, err := l.tun.Read(buf[start : len(buf)-tagLen-l.codec.padding])
		if err != nil {
			if !l.closed() && !errors.Is(err, os.ErrClosed) {
				log.Printf("obfs %s: read: %v", l.cfg.Name, err)
			}
			return
		}
		pkt := buf[off : start+n]
		ts := uint32(time.Now().Unix())
		k := 1
		if !l.vnet {
			out[0] = l.codec.seal(buf, typeData, pkt, ts)
		} else if h := offload.ParseHdr(buf[start:]); h.GSOType == offload.GSONone {
			if offload.Complete(pkt, h) != nil {
				continue
			}
			out[0] = l.codec.seal(buf, typeData, pkt, ts)
		} else {
			nseg, size := offload.Segments(pkt, h)
			if nseg == 0 || nseg > maxSegs {
				continue
			}
			for i := range segs[:nseg] {
				if len(segs[i]) < l.codec.room(size) {
					segs[i] = make([]byte, l.codec.room(size))
				}
			}
			if k, err = offload.Segment(pkt, h, 0, segs, off, sizes); err != nil {
				continue
			}
			for i := range k {
				out[i] = l.codec.seal(segs[i], typeData, segs[i][off:off+sizes[i]], ts)
			}
		}
		if l.write(out[:k], v6) == nil {
			l.sent.Store(time.Now().UnixNano())
		}
	}
}

// write sends the datagrams of one read: as one UDP GSO send when they are
// the same size (no padding), else in one sendmmsg.
func (l *Link) write(out [][]byte, v6 bool) error {
	to := *l.peer.Load()
	if len(out) > 1 && !l.noGSO && sameSize(out) {
		if err := l.w.WriteGSO(out, to, v6); err == nil {
			return nil
		} else {
			l.noGSO = true
			log.Printf("obfs %s: UDP GSO unavailable (%v); sending datagrams one by one", l.cfg.Name, err)
		}
	}
	return l.w.Write(out, to, v6)
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

// inbound: UDP -> authenticate, decrypt -> TUN, a batch of datagrams per
// system call; consecutive TCP segments of a flow go to the interface as
// one super-packet. Any address that proves it holds the key becomes the
// far end, so a NAT rebinding doesn't break us.
func (l *Link) inbound() {
	defer l.wg.Done()
	bufs, sizes, from := make([][]byte, batch), make([]int, batch), make([]netip.AddrPort, batch)
	for i := range bufs {
		bufs[i] = make([]byte, maxDatagram)
	}
	off := nonceLen + headerLen
	var gro offload.Coalescer
	write := func(b []byte) error { l.tun.Write(b); return nil }
	for {
		k, err := l.r.Read(bufs, sizes, from)
		if err != nil {
			if !l.closed() {
				log.Printf("obfs %s: receive: %v", l.cfg.Name, err)
			}
			return
		}
		var peer netip.AddrPort
		for i := range k {
			if sizes[i] < 0 {
				l.BadDatagrams.Add(1)
				continue
			}
			typ, pkt, err := l.codec.open(bufs[i][:sizes[i]])
			if err != nil {
				l.BadDatagrams.Add(1)
				continue
			}
			peer = from[i]
			if typ != typeData || len(pkt) == 0 {
				continue
			}
			if l.vnet {
				gro.Add(bufs[i][off-offload.HdrLen : off+len(pkt)])
			} else {
				l.tun.Write(pkt)
			}
		}
		if l.vnet {
			gro.Flush(write)
		}
		if p := l.peer.Load(); peer.IsValid() && *p != peer {
			l.peer.Store(&peer)
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
