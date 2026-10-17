package tcptun

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"neotunnel/internal/config"
)

// fakeDev is a TUN stand-in: packets pushed into in are read by the link,
// packets the link writes arrive on got.
type fakeDev struct {
	in   chan []byte
	got  chan []byte
	once sync.Once
	done chan struct{}
}

func newDev() *fakeDev {
	return &fakeDev{in: make(chan []byte, 64), got: make(chan []byte, 64), done: make(chan struct{})}
}

func (d *fakeDev) Read(b []byte) (int, error) {
	select {
	case p := <-d.in:
		return copy(b, p), nil
	case <-d.done:
		return 0, os.ErrClosed
	}
}

func (d *fakeDev) Write(b []byte) (int, error) {
	select {
	case d.got <- append([]byte(nil), b...):
	default:
	}
	return len(b), nil
}

func (d *fakeDev) Close() error { d.once.Do(func() { close(d.done) }); return nil }

func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

type pair struct {
	a, b   *Link
	da, db *fakeDev
}

func startPair(t *testing.T, secretA, secretB, cipher string, ka time.Duration) pair {
	t.Helper()
	pa, pb := freePort(t), freePort(t)
	lo := netip.MustParseAddr("127.0.0.1")
	da, db := newDev(), newDev()
	a, err := start(Config{Name: "a", Tunnel: "t1", Secret: secretA, Side: 0, Remote: netip.AddrPortFrom(lo, pb), Port: pa, Cipher: cipher, Keepalive: ka}, da)
	if err != nil {
		t.Fatal(err)
	}
	b, err := start(Config{Name: "b", Tunnel: "t1", Secret: secretB, Side: 1, Remote: netip.AddrPortFrom(lo, pa), Port: pb, Cipher: cipher, Keepalive: ka}, db)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return pair{a, b, da, db}
}

func waitConnected(t *testing.T, ls ...*Link) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, l := range ls {
			ok = ok && l.Connected()
		}
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("links did not connect")
}

func expect(t *testing.T, d *fakeDev, want []byte) {
	t.Helper()
	select {
	case got := <-d.got:
		if !bytes.Equal(got, want) {
			t.Fatalf("got %x, want %x", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("packet %x did not arrive", want[:min(len(want), 8)])
	}
}

func TestCarriesPacketsBothWays(t *testing.T) {
	for _, c := range []string{"", "aes-gcm-128", "aes-gcm-256", "chacha20-poly1305"} {
		t.Run("cipher="+c, func(t *testing.T) {
			s := config.NewSecret()
			p := startPair(t, s, s, c, 0)
			waitConnected(t, p.a, p.b)
			for i := 0; i < 50; i++ {
				pkt := bytes.Repeat([]byte{byte(i)}, 20+i*29)
				p.da.in <- pkt
				expect(t, p.db, pkt)
				p.db.in <- pkt
				expect(t, p.da, pkt)
			}
			if n := p.a.count() + p.b.count(); n != 2 {
				t.Errorf("want one connection (2 ends), got %d ends", n)
			}
		})
	}
}

func TestWrongSecretIsRefused(t *testing.T) {
	p := startPair(t, config.NewSecret(), config.NewSecret(), "", 0)
	time.Sleep(4 * time.Second) // past side 0's dial grace
	if p.a.Connected() || p.b.Connected() {
		t.Fatal("links with different secrets connected")
	}
	if p.a.BadHandshakes.Load()+p.b.BadHandshakes.Load() == 0 {
		t.Error("no handshake was refused")
	}
}

func TestStrangerIsRefused(t *testing.T) {
	s := config.NewSecret()
	p := startPair(t, s, s, "", 0)
	waitConnected(t, p.a, p.b)
	nc, err := net.Dial("tcp4", p.a.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	hello := make([]byte, 2+nonceSize)
	hello[0], hello[1] = version, 1
	nc.Write(hello)
	resp := make([]byte, nonceSize+macSize)
	io.ReadFull(nc, resp)
	nc.Write(make([]byte, macSize)) // a wrong proof
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := nc.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection with a wrong proof stayed open")
	}
	if p.a.count() != 1 {
		t.Errorf("stranger was added: %d connections", p.a.count())
	}
}

func TestReconnects(t *testing.T) {
	s := config.NewSecret()
	p := startPair(t, s, s, "chacha20-poly1305", 0)
	waitConnected(t, p.a, p.b)
	// Cut the connection under both ends.
	p.b.mu.Lock()
	for c := range p.b.conns {
		c.nc.Close()
	}
	p.b.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	waitConnected(t, p.a, p.b)
	pkt := []byte("after the reconnect")
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.da.in <- pkt
		select {
		case got := <-p.db.got:
			if !bytes.Equal(got, pkt) {
				t.Fatalf("got %q", got)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("no traffic after reconnecting")
		}
	}
}

func TestSilentConnectionTimesOut(t *testing.T) {
	s := config.NewSecret()
	k, err := deriveKeys(Config{Tunnel: "t1", Secret: s, Side: 1})
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	dev := newDev()
	l, err := start(Config{Name: "a", Tunnel: "t1", Secret: s, Side: 0, Remote: netip.MustParseAddrPort("127.0.0.1:9"), Port: port, Keepalive: 200 * time.Millisecond}, dev)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	nc, err := net.Dial("tcp4", l.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if _, err := dialHandshake(nc, k); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, l)
	// We never answer its keepalives: within 3 of them it gives up.
	deadline := time.Now().Add(3 * time.Second)
	for l.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("a silent connection was kept")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTamperedFrameIsRejected(t *testing.T) {
	ka := &keys{auth: []byte("x"), cipher: "aes-gcm-256", send: bytes.Repeat([]byte{1}, 32), recv: bytes.Repeat([]byte{2}, 32)}
	kb := &keys{auth: []byte("x"), cipher: "aes-gcm-256", send: ka.recv, recv: ka.send}
	nd, nl := bytes.Repeat([]byte{3}, nonceSize), bytes.Repeat([]byte{4}, nonceSize)
	sa, _ := ka.session(nd, nl)
	sb, _ := kb.session(nd, nl)
	var wire bytes.Buffer
	w := bufioWriter(&wire)
	sa.writeFrame(w, []byte("hello"))
	w.Flush()
	frame := wire.Bytes()
	buf := make([]byte, maxFrame)
	if got, err := sb.readFrame(bufioReader(bytes.NewReader(frame)), buf); err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	frame[4] ^= 1
	sb.recvN = 0
	if _, err := sb.readFrame(bufioReader(bytes.NewReader(frame)), buf); err == nil {
		t.Fatal("a tampered frame was accepted")
	}
}

func bufioWriter(w io.Writer) *bufio.Writer { return bufio.NewWriter(w) }
func bufioReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

// When side 1 can't reach side 0, side 0 dials after its grace period.
func TestOneWayPath(t *testing.T) {
	s := config.NewSecret()
	pa, pb := freePort(t), freePort(t)
	lo := netip.MustParseAddr("127.0.0.1")
	da, db := newDev(), newDev()
	a, err := start(Config{Name: "a", Tunnel: "t1", Secret: s, Side: 0, Remote: netip.AddrPortFrom(lo, pb), Port: pa}, da)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := start(Config{Name: "b", Tunnel: "t1", Secret: s, Side: 1, Remote: netip.AddrPortFrom(lo, freePort(t)), Port: pb}, db)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitConnected(t, a, b)
	pkt := []byte("one way")
	db.in <- pkt
	expect(t, da, pkt)
}

// With several streams, side 1 opens all of them, both ends send on the
// same set, and every flow arrives, each in order.
func TestStreams(t *testing.T) {
	s := config.NewSecret()
	pa, pb := freePort(t), freePort(t)
	lo := netip.MustParseAddr("127.0.0.1")
	da, db := newDev(), newDev()
	a, err := start(Config{Name: "a", Tunnel: "t1", Secret: s, Side: 0, Remote: netip.AddrPortFrom(lo, pb), Port: pa, Streams: 4}, da)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := start(Config{Name: "b", Tunnel: "t1", Secret: s, Side: 1, Remote: netip.AddrPortFrom(lo, pa), Port: pb, Streams: 4}, db)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	deadline := time.Now().Add(5 * time.Second)
	for a.count() < 4 || b.count() < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("connections: a %d, b %d", a.count(), b.count())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Eight UDP flows, three packets each.
	pkt := func(flow, seq byte) []byte {
		p := make([]byte, 28)
		p[0], p[9] = 0x45, 17
		copy(p[12:16], []byte{10, 0, 0, 1})
		copy(p[16:20], []byte{10, 0, 0, 2})
		p[20], p[21], p[22], p[23] = 0x30, flow, 0x01, 0xbb
		p[27] = seq
		return p
	}
	for seq := byte(0); seq < 3; seq++ {
		for f := byte(0); f < 8; f++ {
			da.in <- pkt(f, seq)
		}
	}
	last := map[byte]int{}
	for i := 0; i < 24; i++ {
		select {
		case got := <-db.got:
			f, seq := got[21], int(got[27])
			if prev, ok := last[f]; ok && seq != prev+1 {
				t.Fatalf("flow %d out of order: %d after %d", f, seq, prev)
			}
			last[f] = seq
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of 24 packets arrived", i)
		}
	}
}

func TestFlowHash(t *testing.T) {
	a := []byte{0x45, 0, 0, 40, 0, 0, 0x40, 0, 64, 6, 0, 0, 10, 0, 0, 1, 10, 0, 0, 2, 0x30, 0x39, 0x01, 0xbb}
	b := append([]byte(nil), a...)
	b[21] = 0x3a // another source port
	if flowHash(a) != flowHash(append([]byte(nil), a...)) {
		t.Error("same flow, different hash")
	}
	if flowHash(a) == flowHash(b) {
		t.Error("different flows, same hash")
	}
	frag := append([]byte(nil), a...)
	frag[7] = 1 // a later fragment carries no ports
	other := append([]byte(nil), frag...)
	other[21] = 0x3a
	if flowHash(frag) != flowHash(other) {
		t.Error("fragments hashed by bytes that are not ports")
	}
}
