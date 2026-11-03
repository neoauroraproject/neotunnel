package mtr

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func echoHdr(typ byte, id, seq uint16) []byte {
	b := make([]byte, 8)
	b[0] = typ
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	return b
}

func ip4(proto byte, payload []byte) []byte {
	h := make([]byte, 20)
	h[0], h[9] = 0x45, proto
	return append(h, payload...)
}

func TestParse(t *testing.T) {
	// IPv4: an echo reply, a time exceeded quoting our probe, someone
	// else's reply.
	if seq, echo, ok := parse(ip4(1, echoHdr(0, 7, 42)), false, 7); !ok || !echo || seq != 42 {
		t.Errorf("v4 echo reply: %d %v %v", seq, echo, ok)
	}
	exceeded := ip4(1, append(make([]byte, 8), ip4(1, echoHdr(8, 7, 43))...))
	exceeded[20] = 11
	if seq, echo, ok := parse(exceeded, false, 7); !ok || echo || seq != 43 {
		t.Errorf("v4 time exceeded: %d %v %v", seq, echo, ok)
	}
	if _, _, ok := parse(ip4(1, echoHdr(0, 8, 42)), false, 7); ok {
		t.Error("another program's reply taken")
	}
	// IPv6 (no IP header on the socket): time exceeded quotes the IPv6
	// header (next header 58) and our echo request.
	q := make([]byte, 40)
	q[6] = 58
	ex6 := append(append([]byte{3, 0, 0, 0, 0, 0, 0, 0}, q...), echoHdr(128, 7, 44)...)
	if seq, echo, ok := parse(ex6, true, 7); !ok || echo || seq != 44 {
		t.Errorf("v6 time exceeded: %d %v %v", seq, echo, ok)
	}
	if seq, echo, ok := parse(echoHdr(129, 7, 45), true, 7); !ok || !echo || seq != 45 {
		t.Errorf("v6 echo reply: %d %v %v", seq, echo, ok)
	}
	if _, _, ok := parse([]byte{1, 2}, false, 7); ok {
		t.Error("short packet taken")
	}
}

// Hops up to where the target answered, each with its main address, loss
// and times; a silent hop shows all lost.
func TestSummarize(t *testing.T) {
	target := netip.MustParseAddr("198.51.100.9")
	r1, r1b, r3 := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), target
	sent := map[int]int{1: 4, 2: 4, 3: 4, 4: 2}
	replies := []reply{
		{ttl: 1, from: r1, rtt: 10}, {ttl: 1, from: r1, rtt: 20}, {ttl: 1, from: r1b, rtt: 30}, {ttl: 1, from: r1, rtt: 20},
		{ttl: 3, from: r3, rtt: 50, echo: true}, {ttl: 3, from: r3, rtt: 70, echo: true},
		{ttl: 4, from: r3, rtt: 60, echo: true},
	}
	r := summarize(target, 4, sent, replies)
	if !r.Reached || len(r.Hops) != 3 {
		t.Fatalf("%+v", r)
	}
	h := r.Hops[0]
	if h.Host != "10.0.0.1" || len(h.Others) != 1 || h.Got != 4 || h.Loss != 0 || h.Best != 10 || h.Worst != 30 || h.Avg != 20 {
		t.Errorf("hop 1 %+v", h)
	}
	if h := r.Hops[1]; h.Host != "" || h.Loss != 100 {
		t.Errorf("hop 2 %+v", h)
	}
	if h := r.Hops[2]; h.Host != target.String() || h.Loss != 50 || h.Avg != 60 {
		t.Errorf("hop 3 %+v", h)
	}
}
