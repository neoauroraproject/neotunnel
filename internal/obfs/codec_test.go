package obfs

import (
	"bytes"
	"testing"
	"time"
)

func pair(t *testing.T, padding int) (*codec, *codec) {
	t.Helper()
	k1, k2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	a, err := newCodec(k1, k2, padding)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newCodec(k2, k1, padding)
	return a, b
}

func now() uint32 { return uint32(time.Now().Unix()) }

func TestRoundTripAndPadding(t *testing.T) {
	a, b := pair(t, 64)
	pkt := []byte("an IP packet")
	sizes := map[int]bool{}
	for i := 0; i < 50; i++ {
		buf := make([]byte, a.room(len(pkt)))
		d := a.seal(buf, typeData, pkt, now())
		sizes[len(d)] = true
		typ, got, err := b.open(append([]byte(nil), d...))
		if err != nil || typ != typeData || !bytes.Equal(got, pkt) {
			t.Fatalf("round trip: %v %d %q", err, typ, got)
		}
	}
	if len(sizes) < 5 {
		t.Errorf("padding should vary datagram sizes, saw %d sizes", len(sizes))
	}
}

func TestNoFixedBytes(t *testing.T) {
	a, _ := pair(t, 0)
	first := map[byte]bool{}
	for i := 0; i < 200; i++ {
		d := a.seal(make([]byte, a.room(4)), typeData, []byte{1, 2, 3, 4}, now())
		first[d[0]] = true
	}
	if len(first) < 100 {
		t.Errorf("first byte looks fixed: %d distinct values", len(first))
	}
}

func TestRejectsReplayTamperAndWrongKey(t *testing.T) {
	a, b := pair(t, 0)
	d := a.seal(make([]byte, a.room(3)), typeData, []byte("abc"), now())
	if _, _, err := b.open(append([]byte(nil), d...)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.open(append([]byte(nil), d...)); err == nil {
		t.Error("replayed datagram accepted")
	}
	d2 := a.seal(make([]byte, a.room(3)), typeData, []byte("abc"), now())
	d2[len(d2)-1] ^= 1
	if _, _, err := b.open(d2); err == nil {
		t.Error("tampered datagram accepted")
	}
	_, c := pair(t, 0)
	c.recv = a.recv // a's own receive key can't open a's datagrams
	d3 := a.seal(make([]byte, a.room(3)), typeData, []byte("abc"), now())
	if _, _, err := c.open(d3); err == nil {
		t.Error("datagram opened with the wrong key")
	}
}

func TestStaleFirstDatagramRejected(t *testing.T) {
	a, b := pair(t, 0)
	old := uint32(time.Now().Add(-time.Hour).Unix())
	if _, _, err := b.open(a.seal(make([]byte, a.room(1)), typeData, []byte{1}, old)); err == nil {
		t.Error("an hour old datagram accepted by a fresh receiver")
	}
}

func TestWindowReordering(t *testing.T) {
	var w window
	for _, s := range []uint64{100, 103, 101, 102} {
		if !w.accept(s) {
			t.Fatalf("in-window packet %d rejected", s)
		}
	}
	if w.accept(101) {
		t.Error("duplicate accepted")
	}
	if !w.accept(100 + windowSize*3) {
		t.Error("big jump forward rejected")
	}
	if w.accept(104) {
		t.Error("packet behind the window accepted")
	}
}
