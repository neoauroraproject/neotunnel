package tcptun

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"neotunnel/internal/tun"
)

// Config describes this node's end of a TCP tunnel.
type Config struct {
	Name      string // interface name
	Tunnel    string // tunnel name (keys are bound to it)
	Secret    string
	Side      int
	Remote    netip.AddrPort // where the far end listens
	Port      uint16         // where this end listens
	Cipher    string         // "" = authenticated handshake, plain frames
	Keepalive time.Duration  // 0 = off (TCP keepalive only)
	// Streams is how many TCP connections carry the tunnel (0 = 1). Each
	// inner flow sticks to one of them, so packets of a flow stay in order
	// while a loss on one connection slows only the flows on it.
	Streams int
}

func (c Config) streams() int { return max(1, min(c.Streams, 16)) }

const (
	// dialGrace is how long side 0 waits for side 1 to connect before it
	// dials itself.
	dialGrace  = 3 * time.Second
	maxBackoff = 30 * time.Second
	// bufSize holds the largest inner MTU NeoTunnel allows (9000).
	bufSize    = 9216
	queueLen   = 512     // packets waiting per connection
	queueBytes = 1 << 20 // and their bytes (a super-packet is up to 64 KiB)
	batchBytes = 128 << 10
	maxPending = 16 // handshakes in progress
	vnetLen    = tun.VnetHdrLen
	// bigRead holds a virtio-net header and a 64 KiB super-packet.
	bigRead     = vnetLen + 65535
	tcpKeepIdle = 15 * time.Second
)

// Link is a running tunnel end: a TUN interface, a listener and the
// connections to the far end.
type Link struct {
	cfg  Config
	keys *keys
	// devs are the interface's queues: one reader each, and each connection
	// writes to one (the kernel spreads flows over them, so several CPUs
	// move packets at once).
	devs []io.ReadWriteCloser
	ln   net.Listener
	pool sync.Pool // packet buffers (bufSize, plus a header with vnet)
	big  sync.Pool // super-packet buffers (bigRead)
	// vnet: the interface puts a virtio-net header in front of packets, so
	// it can hand out super-packets once offload is on (see the package
	// comment); offload turns that on and off, nil on a test device.
	vnet    bool
	offload func(on bool) error
	offOn   bool // under mu
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu    sync.Mutex
	conns map[*conn]bool
	send  atomic.Pointer[[]*conn] // connections packets go out on
	want  atomic.Int32            // connections to keep (Config.Streams, changeable)
	gone  chan struct{}           // a connection closed
	nextQ int                     // queue for the next connection

	Connects       atomic.Uint64
	BadHandshakes  atomic.Uint64
	DroppedPackets atomic.Uint64 // no connection, or too large
	SuperPackets   atomic.Uint64 // super-packets sent whole
}

type conn struct {
	nc        net.Conn
	s         *session
	preferred bool // dialed by side 1: both ends send on it when they have it
	since     time.Time
	mu        sync.Mutex // writes
	w         *bufio.Writer
	dev       io.Writer   // interface queue its packets go to
	q         chan []byte // packets for this connection
	qBytes    atomic.Int64
	done      chan struct{}
	sent      atomic.Int64 // unix nanos of the last flush
	closeOnce sync.Once
}

// Start opens the interface and the listener and starts connecting.
func Start(c Config) (*Link, error) {
	if _, err := deriveKeys(c); err != nil {
		return nil, err
	}
	// With a virtio-net header when the kernel has it (all current ones),
	// for super-packets; plain otherwise.
	if qs, err := tun.OpenQueues(c.Name, queues()); err == nil {
		devs := make([]io.ReadWriteCloser, len(qs))
		for i, q := range qs {
			devs[i] = q
		}
		l, err := startDev(c, devs, true, func(on bool) error { return tun.SetOffload(qs[0], on) })
		if err != nil {
			for _, q := range qs {
				q.Close()
			}
		}
		return l, err
	}
	dev, err := tun.Open(c.Name)
	if err != nil {
		return nil, err
	}
	l, err := start(c, dev)
	if err != nil {
		dev.Close()
	}
	return l, err
}

func (c Config) network() string {
	if c.Remote.Addr().Is6() {
		return "tcp6"
	}
	return "tcp4"
}

// queues is how many interface queues a link opens: one per CPU, up to 8.
func queues() int { return max(1, min(runtime.NumCPU(), 8)) }

func start(c Config, dev io.ReadWriteCloser) (*Link, error) {
	return startDev(c, []io.ReadWriteCloser{dev}, false, nil)
}

func startDev(c Config, devs []io.ReadWriteCloser, vnet bool, offload func(bool) error) (*Link, error) {
	k, err := deriveKeys(c)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen(c.network(), fmt.Sprintf(":%d", c.Port))
	if err != nil {
		return nil, fmt.Errorf("listen on TCP %d: %w", c.Port, err)
	}
	l := &Link{cfg: c, keys: k, devs: devs, ln: ln, conns: map[*conn]bool{}, gone: make(chan struct{}, 1), vnet: vnet, offload: offload}
	small := bufSize
	if vnet {
		small += vnetLen
	}
	l.pool.New = func() any { b := make([]byte, small); return &b }
	l.big.New = func() any { b := make([]byte, bigRead); return &b }
	l.want.Store(int32(c.streams()))
	l.ctx, l.cancel = context.WithCancel(context.Background())
	l.wg.Add(2 + len(devs))
	for _, d := range devs {
		go l.fromDevice(d)
	}
	go l.accept()
	go l.dial()
	return l, nil
}

// Close stops the link; its interface disappears.
func (l *Link) Close() {
	if l.ctx.Err() != nil {
		return
	}
	l.cancel()
	l.ln.Close()
	for _, d := range l.devs {
		d.Close()
	}
	l.mu.Lock()
	for c := range l.conns {
		c.close()
	}
	l.mu.Unlock()
	l.wg.Wait()
}

func (l *Link) closed() bool { return l.ctx.Err() != nil }

func (l *Link) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-l.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (c *conn) close() { c.closeOnce.Do(func() { c.nc.Close(); close(c.done) }) }

// Connected reports whether a connection to the far end is up.
func (l *Link) Connected() bool { s := l.send.Load(); return s != nil && len(*s) > 0 }

func (l *Link) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns)
}

// elect picks the connections to send on: those side 1 dialed when there
// are any, so both ends use the same ones, else all; oldest first, so the
// flow-to-connection mapping only moves when the set changes. Call with mu
// held.
func (l *Link) elect() {
	var pref, all []*conn
	for c := range l.conns {
		all = append(all, c)
		if c.preferred {
			pref = append(pref, c)
		}
	}
	set := all
	if len(pref) > 0 {
		set = pref
	}
	sort.Slice(set, func(i, j int) bool { return set[i].since.Before(set[j].since) })
	l.send.Store(&set)
	l.updateOffload()
}

// updateOffload has the interface hand out super-packets while every
// connection packets go out on takes them, and ordinary packets otherwise.
// Call with mu held.
func (l *Link) updateOffload() {
	if l.offload == nil || l.closed() {
		return
	}
	set := *l.send.Load()
	on := len(set) > 0
	for _, c := range set {
		on = on && c.s.txBig.Load()
	}
	if on == l.offOn {
		return
	}
	if err := l.offload(on); err != nil {
		log.Printf("tcp %s: offload: %v", l.cfg.Name, err)
		return
	}
	l.offOn = on
}

func (l *Link) add(nc net.Conn, s *session, dialed bool) {
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(tcpKeepIdle)
		useBBR(tc)
	}
	c := &conn{nc: nc, s: s, since: time.Now(), w: bufio.NewWriterSize(nc, 64<<10),
		q: make(chan []byte, queueLen), done: make(chan struct{}),
		preferred: dialed == (l.cfg.Side == 1)}
	c.sent.Store(time.Now().UnixNano())
	l.mu.Lock()
	if l.closed() {
		l.mu.Unlock()
		nc.Close()
		return
	}
	c.dev = l.devs[l.nextQ%len(l.devs)]
	l.nextQ++
	l.conns[c] = true
	l.elect()
	l.wg.Add(2)
	go l.toConn(c)
	if l.cfg.Keepalive > 0 {
		l.wg.Add(1)
		go l.keepalive(c)
	}
	l.mu.Unlock()
	l.Connects.Add(1)
	log.Printf("tcp %s: connected (%s)", l.cfg.Name, nc.RemoteAddr())
	go l.fromConn(c)
}

func (l *Link) remove(c *conn, why error) {
	c.close()
	l.mu.Lock()
	if !l.conns[c] {
		l.mu.Unlock()
		return
	}
	delete(l.conns, c)
	l.elect()
	l.mu.Unlock()
	if !l.closed() {
		log.Printf("tcp %s: connection to %s lost: %v", l.cfg.Name, c.nc.RemoteAddr(), why)
	}
	select {
	case l.gone <- struct{}{}:
	default:
	}
}

// dial keeps a connection to the far end whenever there is none.
func (l *Link) dial() {
	defer l.wg.Done()
	d := net.Dialer{Timeout: handshakeTimeout, KeepAlive: tcpKeepIdle}
	backoff, failing := time.Second, false
	for {
		for l.count() >= int(l.want.Load()) {
			select {
			case <-l.ctx.Done():
				return
			case <-l.gone:
			}
		}
		// Side 1 opens the connections; side 0 only when side 1 has not
		// managed to within the grace period (it may not reach us).
		if l.cfg.Side == 0 && l.count() == 0 && (!l.sleep(dialGrace) || l.count() > 0) {
			if l.closed() {
				return
			}
			continue
		}
		if l.cfg.Side == 0 && l.count() > 0 && !l.dialedAny() {
			// Side 1 is connecting; let it bring the rest.
			select {
			case <-l.ctx.Done():
				return
			case <-l.gone:
			case <-time.After(dialGrace):
			}
			continue
		}
		nc, err := d.DialContext(l.ctx, l.cfg.network(), l.cfg.Remote.String())
		var s *session
		if err == nil {
			if s, err = dialHandshake(nc, l.keys); err != nil {
				nc.Close()
			}
		}
		if l.closed() {
			if err == nil {
				nc.Close()
			}
			return
		}
		if err != nil {
			if !failing {
				log.Printf("tcp %s: cannot connect to %s: %v (retrying)", l.cfg.Name, l.cfg.Remote, err)
				failing = true
			}
			if !l.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff, failing = time.Second, false
		l.add(nc, s, true)
	}
}

// accept takes connections from the far end.
func (l *Link) accept() {
	defer l.wg.Done()
	pending := make(chan struct{}, maxPending)
	for {
		nc, err := l.ln.Accept()
		if err != nil {
			if l.closed() {
				return
			}
			if !l.sleep(100 * time.Millisecond) {
				return
			}
			continue
		}
		select {
		case pending <- struct{}{}:
		default:
			nc.Close() // too many half-open connections
			continue
		}
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			defer func() { <-pending }()
			s, err := acceptHandshake(nc, l.keys)
			if err != nil {
				l.BadHandshakes.Add(1)
				nc.Close()
				return
			}
			l.add(nc, s, false)
		}()
	}
}

// fromDevice queues the packets the kernel routes into the interface.
func (l *Link) fromDevice(dev io.Reader) {
	defer l.wg.Done()
	// With vnet a read may bring a super-packet, so it goes into a 64 KiB
	// buffer; an ordinary packet is copied out to a small one so queued
	// packets don't each hold 64 KiB.
	var rb *[]byte
	if l.vnet {
		rb = l.big.Get().(*[]byte)
	}
	for {
		bp := rb
		if !l.vnet {
			bp = l.pool.Get().(*[]byte)
		}
		n, err := dev.Read(*bp)
		if err != nil {
			if !l.closed() && !errors.Is(err, os.ErrClosed) {
				log.Printf("tcp %s: read: %v", l.cfg.Name, err)
			}
			return
		}
		p := (*bp)[:n]
		if l.vnet {
			if n <= bufSize+vnetLen {
				sp := l.pool.Get().(*[]byte)
				p = (*sp)[:copy(*sp, p)]
			} else {
				rb = l.big.Get().(*[]byte)
			}
		}
		set := l.send.Load()
		if set == nil || len(*set) == 0 {
			l.DroppedPackets.Add(1)
			l.put(p)
			continue
		}
		ip := p
		if l.vnet {
			ip = p[vnetLen:]
		}
		c := (*set)[flowHash(ip)%uint32(len(*set))]
		// A full queue drops, like a router: the inner flow backs off
		// without holding up the flows on other connections.
		if c.qBytes.Load()+int64(len(p)) > queueBytes {
			l.DroppedPackets.Add(1)
			l.put(p)
			continue
		}
		select {
		case c.q <- p:
			c.qBytes.Add(int64(len(p)))
		default:
			l.DroppedPackets.Add(1)
			l.put(p)
		}
	}
}

// SetStreams changes how many connections carry the tunnel, without
// interrupting it: more are opened, or the newest this end opened are
// closed (their flows move to the others).
func (l *Link) SetStreams(n int) {
	n = Config{Streams: n}.streams()
	l.want.Store(int32(n))
	l.mu.Lock()
	var mine []*conn
	for c := range l.conns {
		if c.preferred == (l.cfg.Side == 1) {
			mine = append(mine, c)
		}
	}
	extra := len(l.conns) - n
	l.mu.Unlock()
	sort.Slice(mine, func(i, j int) bool { return mine[i].since.After(mine[j].since) })
	for i := 0; i < extra && i < len(mine); i++ {
		l.remove(mine[i], errors.New("fewer streams"))
	}
	select {
	case l.gone <- struct{}{}: // wake the dialer
	default:
	}
}

// dialedAny reports whether this end opened any of the connections.
func (l *Link) dialedAny() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for c := range l.conns {
		if c.preferred == (l.cfg.Side == 1) {
			return true
		}
	}
	return false
}

func (l *Link) put(p []byte) {
	p = p[:cap(p)]
	if cap(p) >= bigRead {
		l.big.Put(&p)
		return
	}
	l.pool.Put(&p)
}

// Control frames (see the package comment).
var (
	ctlGSO    = []byte("\x00NTG")
	ctlSwitch = []byte("\x00NTS")
)

// writePacket buffers one queued packet the way the connection takes it:
// with its virtio-net header after the switch, without it before (a
// super-packet that can't go is dropped; that only happens while offload
// is turning off). Call with c.mu held.
func (l *Link) writePacket(c *conn, p []byte) error {
	if l.vnet {
		if c.s.txBig.Load() {
			if p[1] != 0 { // gso_type
				l.SuperPackets.Add(1)
			}
		} else {
			if p[0]&1 != 0 || p[1] != 0 { // checksum left to do, or GSO
				l.DroppedPackets.Add(1)
				return nil
			}
			p = p[vnetLen:]
		}
	}
	if len(p) > c.s.maxPacket() {
		l.DroppedPackets.Add(1)
		return nil
	}
	return c.s.writeFrame(c.w, p)
}

func (l *Link) dequeued(c *conn, p []byte) {
	c.qBytes.Add(-int64(len(p)))
	l.put(p)
}

// control handles a control frame from the far end.
func (l *Link) control(c *conn, msg []byte) {
	switch string(msg) {
	case string(ctlGSO):
		if !l.vnet || c.s.txBig.Load() {
			return
		}
		c.mu.Lock()
		c.nc.SetWriteDeadline(time.Now().Add(l.writeTimeout()))
		err := c.s.writeFrame(c.w, ctlSwitch)
		if err == nil {
			c.s.txBig.Store(true)
			err = c.w.Flush()
		}
		c.mu.Unlock()
		if err != nil {
			l.remove(c, err)
			return
		}
		l.mu.Lock()
		l.updateOffload()
		l.mu.Unlock()
	case string(ctlSwitch):
		c.s.rxBig = true
	}
}

// toConn writes a connection's queued packets, as many as are waiting per
// flush: one write per burst instead of one per packet.
func (l *Link) toConn(c *conn) {
	defer l.wg.Done()
	if l.vnet {
		c.mu.Lock()
		c.nc.SetWriteDeadline(time.Now().Add(l.writeTimeout()))
		err := c.s.writeFrame(c.w, ctlGSO)
		if err == nil {
			err = c.w.Flush()
		}
		c.mu.Unlock()
		if err != nil {
			l.remove(c, err)
			l.drain(c)
			return
		}
	}
	for {
		var p []byte
		select {
		case p = <-c.q:
		case <-c.done:
			l.drain(c)
			return
		}
		c.mu.Lock()
		c.nc.SetWriteDeadline(time.Now().Add(l.writeTimeout()))
		err := l.writePacket(c, p)
		l.dequeued(c, p)
	batch:
		for err == nil && c.w.Buffered() < batchBytes {
			select {
			case p = <-c.q:
				err = l.writePacket(c, p)
				l.dequeued(c, p)
			default:
				break batch
			}
		}
		if err == nil {
			err = c.w.Flush()
		}
		c.sent.Store(time.Now().UnixNano())
		c.mu.Unlock()
		if err != nil {
			l.remove(c, err)
			l.drain(c)
			return
		}
	}
}

// drain returns the buffers of a closed connection's queue.
func (l *Link) drain(c *conn) {
	for {
		select {
		case p := <-c.q:
			l.dequeued(c, p)
		default:
			return
		}
	}
}

func (l *Link) writeTimeout() time.Duration {
	return max(3*l.cfg.Keepalive, 30*time.Second)
}

// fromConn delivers a connection's packets to the interface. With
// keepalives on, a connection that stays silent for three of them is dead.
func (l *Link) fromConn(c *conn) {
	defer l.wg.Done()
	r := bufio.NewReaderSize(c.nc, 256<<10)
	// Frames land after room for a virtio-net header, so a packet from a
	// connection that has not switched gets a zero one in place.
	buf := make([]byte, vnetLen+maxBig)
	var deadline time.Time
	for {
		if ka := l.cfg.Keepalive; ka > 0 {
			if now := time.Now(); now.Sub(deadline) > -3*ka+time.Second {
				deadline = now.Add(3 * ka)
				c.nc.SetReadDeadline(deadline)
			}
		}
		pkt, err := c.s.readFrame(r, buf[vnetLen:])
		if err != nil {
			l.remove(c, err)
			return
		}
		switch {
		case len(pkt) == 0:
			continue
		case len(pkt) == 4 && pkt[0] == 0:
			l.control(c, pkt)
			continue
		case l.vnet && !c.s.rxBig:
			clear(buf[:vnetLen])
			pkt = buf[:vnetLen+len(pkt)]
		}
		c.dev.Write(pkt)
	}
}

// keepalive sends an empty frame when a connection has been quiet.
func (l *Link) keepalive(c *conn) {
	defer l.wg.Done()
	t := time.NewTicker(l.cfg.Keepalive / 2)
	defer t.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-t.C:
		}
		l.mu.Lock()
		alive := l.conns[c]
		l.mu.Unlock()
		if !alive {
			return
		}
		if time.Since(time.Unix(0, c.sent.Load())) < l.cfg.Keepalive/2 {
			continue
		}
		c.mu.Lock()
		c.nc.SetWriteDeadline(time.Now().Add(l.writeTimeout()))
		err := c.s.writeFrame(c.w, nil)
		if err == nil {
			err = c.w.Flush()
		}
		c.sent.Store(time.Now().UnixNano())
		c.mu.Unlock()
		if err != nil {
			l.remove(c, err)
			return
		}
	}
}

// Manager runs the TCP tunnels of the daemon.
type Manager struct {
	mu    sync.Mutex
	links map[string]*Link
	cfgs  map[string]Config
}

func NewManager() *Manager { return &Manager{links: map[string]*Link{}, cfgs: map[string]Config{}} }

// Sync starts, restarts (when changed) and stops links to match cfgs.
func (m *Manager) Sync(cfgs []Config) []error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	want := map[string]bool{}
	for _, c := range cfgs {
		want[c.Name] = true
		if cur, ok := m.links[c.Name]; ok {
			old := m.cfgs[c.Name]
			if old == c {
				continue
			}
			// Only the number of connections changed: adjust in place.
			old.Streams = c.Streams
			if old == c {
				cur.SetStreams(c.Streams)
				m.cfgs[c.Name] = c
				continue
			}
			cur.Close()
			delete(m.links, c.Name)
			delete(m.cfgs, c.Name)
		}
		l, err := Start(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("tunnel %s: %w", c.Tunnel, err))
			continue
		}
		m.links[c.Name] = l
		m.cfgs[c.Name] = c
	}
	for name, l := range m.links {
		if !want[name] {
			l.Close()
			delete(m.links, name)
			delete(m.cfgs, name)
		}
	}
	return errs
}

// Close stops every link.
func (m *Manager) Close() { m.Sync(nil) }
