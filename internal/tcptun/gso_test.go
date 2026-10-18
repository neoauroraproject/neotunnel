package tcptun

import (
	"bytes"
	"io"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"neotunnel/internal/config"
)

// startVnet starts two ends whose devices say whether they put a
// virtio-net header in front of packets; it returns each end's offload
// state.
func startVnet(t *testing.T, cipher string, vnetA, vnetB bool) (pair, *atomic.Bool, *atomic.Bool) {
	t.Helper()
	s := config.NewSecret()
	pa, pb := freePort(t), freePort(t)
	lo := netip.MustParseAddr("127.0.0.1")
	da, db := newDev(), newDev()
	var offA, offB atomic.Bool
	hook := func(v bool, o *atomic.Bool) func(bool) error {
		if !v {
			return nil
		}
		return func(on bool) error { o.Store(on); return nil }
	}
	a, err := startDev(Config{Name: "a", Tunnel: "t1", Secret: s, Side: 0, Remote: netip.AddrPortFrom(lo, pb), Port: pa, Cipher: cipher}, []io.ReadWriteCloser{da}, vnetA, hook(vnetA, &offA))
	if err != nil {
		t.Fatal(err)
	}
	b, err := startDev(Config{Name: "b", Tunnel: "t1", Secret: s, Side: 1, Remote: netip.AddrPortFrom(lo, pa), Port: pb, Cipher: cipher}, []io.ReadWriteCloser{db}, vnetB, hook(vnetB, &offB))
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	waitConnected(t, a, b)
	return pair{a, b, da, db}, &offA, &offB
}

func ipPacket(n int, fill byte) []byte {
	p := bytes.Repeat([]byte{fill}, n)
	p[0] = 0x45
	return p
}

func withHdr(gso byte, pkt []byte) []byte {
	h := make([]byte, vnetLen)
	h[1] = gso
	return append(h, pkt...)
}

func waitTrue(t *testing.T, b *atomic.Bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b.Load() {
			return
		}
	}
	t.Fatalf("%s did not happen", what)
}

// Two new ends switch both directions and carry super-packets whole.
func TestGSOBetweenNewEnds(t *testing.T) {
	for _, c := range []string{"", "aes-gcm-128"} {
		t.Run("cipher="+c, func(t *testing.T) {
			p, offA, offB := startVnet(t, c, true, true)
			waitTrue(t, offA, "offload on a")
			waitTrue(t, offB, "offload on b")
			super := withHdr(1, ipPacket(60000, 7)) // VIRTIO_NET_HDR_GSO_TCPV4
			p.da.in <- super
			expect(t, p.db, super)
			p.db.in <- super
			expect(t, p.da, super)
			small := withHdr(0, ipPacket(100, 3))
			p.da.in <- small
			expect(t, p.db, small)
			if p.a.SuperPackets.Load() != 1 || p.b.SuperPackets.Load() != 1 {
				t.Errorf("super-packets sent: a %d, b %d", p.a.SuperPackets.Load(), p.b.SuperPackets.Load())
			}
		})
	}
}

// A new end and an old one keep ordinary frames; the new end never turns
// offload on and adds or strips the header itself.
func TestGSOWithOldEnd(t *testing.T) {
	p, offA, _ := startVnet(t, "chacha20-poly1305", true, false)
	pkt := ipPacket(1400, 5)
	p.da.in <- withHdr(0, pkt)
	expect(t, p.db, pkt)
	p.db.in <- pkt
	expect(t, p.da, withHdr(0, pkt))
	if offA.Load() {
		t.Error("offload turned on with an old far end")
	}
}
