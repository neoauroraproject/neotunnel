package offload

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// superPacket builds a TCP super-packet the way a TUN (l2 == nil) or TAP
// hands one out: lengths for the whole packet, a timestamp option, ACK|PSH,
// and the checksum left to do.
func superPacket(v4 bool, l2 []byte, payload int, seq uint32, sport uint16) ([]byte, Hdr) {
	var ip []byte
	if v4 {
		ip = []byte{0x45, 0, 0, 0, 0x12, 0x34, 0x40, 0, 64, 6, 0, 0, 10, 0, 0, 1, 10, 0, 0, 2}
	} else {
		ip = make([]byte, 40)
		ip[0], ip[6], ip[7] = 0x60, 6, 64
		ip[8], ip[23] = 0xfd, 1
		ip[24], ip[39] = 0xfd, 2
	}
	tcp := make([]byte, 32)
	binary.BigEndian.PutUint16(tcp[0:], sport)
	binary.BigEndian.PutUint16(tcp[2:], 80)
	binary.BigEndian.PutUint32(tcp[4:], seq)
	binary.BigEndian.PutUint32(tcp[8:], 777)
	tcp[12], tcp[13] = 8<<4, tcpACK|tcpPSH
	binary.BigEndian.PutUint16(tcp[14:], 501)
	copy(tcp[20:], []byte{1, 1, 8, 10, 0, 0, 0, 9, 0, 0, 0, 8})
	p := append(append(append([]byte{}, l2...), ip...), tcp...)
	for i := 0; i < payload; i++ {
		p = append(p, byte(i*7))
	}
	l3 := len(l2)
	ipp := p[l3:]
	if v4 {
		binary.BigEndian.PutUint16(ipp[2:], uint16(len(ipp)))
		binary.BigEndian.PutUint16(ipp[10:], ^fold(sum(ipp[:20], 0)))
	} else {
		binary.BigEndian.PutUint16(ipp[4:], uint16(len(ipp)-40))
	}
	ihl := len(ip)
	binary.BigEndian.PutUint16(ipp[ihl+16:], fold(pseudo(ipp, v4, len(ipp)-ihl)))
	h := Hdr{Flags: FlagNeedsCsum, GSOType: GSOTCPv6, HdrLen: uint16(l3 + ihl + 32), GSOSize: 1388,
		CsumStart: uint16(l3 + ihl), CsumOffset: 16}
	if v4 {
		h.GSOType = GSOTCPv4
	}
	return p, h
}

func validTCP(t *testing.T, p []byte, l3 int) {
	t.Helper()
	ip := p[l3:]
	v4 := ip[0]>>4 == 4
	ihl := 40
	if v4 {
		ihl = 20
		if fold(sum(ip[:20], 0)) != 0xffff {
			t.Fatal("bad IPv4 header checksum")
		}
	}
	if fold(sum(ip[ihl:], pseudo(ip, v4, len(ip)-ihl))) != 0xffff {
		t.Fatal("bad TCP checksum")
	}
}

func split(t *testing.T, p []byte, h Hdr, l3 int) [][]byte {
	t.Helper()
	out := make([][]byte, 64)
	for i := range out {
		out[i] = make([]byte, HdrLen+2000)
	}
	sizes := make([]int, 64)
	n, err := Segment(p, h, l3, out, HdrLen, sizes)
	if err != nil {
		t.Fatal(err)
	}
	segs := make([][]byte, n)
	for i := range segs {
		segs[i] = out[i][:HdrLen+sizes[i]]
	}
	return segs
}

func TestSegmentAndCoalesce(t *testing.T) {
	mac := []byte{2, 0, 0, 0, 0, 1, 2, 0, 0, 0, 0, 2, 0x08, 0x00}
	for _, tc := range []struct {
		name string
		v4   bool
		l2   []byte
	}{{"tun4", true, nil}, {"tun6", false, nil}, {"tap4", true, mac}, {"tap6", false, append(append([]byte{}, mac[:12]...), 0x86, 0xdd)}} {
		t.Run(tc.name, func(t *testing.T) {
			l3 := len(tc.l2)
			p, h := superPacket(tc.v4, tc.l2, 20000, 1000, 5555)
			segs := split(t, p, h, l3)
			if len(segs) != 15 {
				t.Fatalf("%d segments, want 15", len(segs))
			}
			for i, s := range segs {
				pkt := s[HdrLen:]
				validTCP(t, pkt, l3)
				ihl := 40
				if tc.v4 {
					ihl = 20
				}
				tcp := pkt[l3+ihl:]
				if got := binary.BigEndian.Uint32(tcp[4:]); got != 1000+uint32(i*1388) {
					t.Fatalf("segment %d seq %d", i, got)
				}
				if psh := tcp[13]&tcpPSH != 0; psh != (i == len(segs)-1) {
					t.Fatalf("segment %d PSH %v", i, psh)
				}
			}
			c := Coalescer{L3: l3, Verify: true}
			for _, s := range segs {
				c.Add(s)
			}
			var got [][]byte
			c.Flush(func(b []byte) error { got = append(got, append([]byte(nil), b...)); return nil })
			if len(got) != 1 {
				t.Fatalf("%d packets after merging, want 1", len(got))
			}
			mh := ParseHdr(got[0])
			if mh.GSOSize != 1388 || mh.Flags != FlagNeedsCsum {
				t.Fatalf("header %+v", mh)
			}
			// Splitting the merged packet again gives the same segments.
			again := split(t, got[0][HdrLen:], mh, l3)
			if len(again) != len(segs) {
				t.Fatalf("%d segments again", len(again))
			}
			for i := range segs {
				if !bytes.Equal(again[i][HdrLen:], segs[i][HdrLen:]) {
					t.Fatalf("segment %d differs after a round trip", i)
				}
			}
			// The merged packet's checksum, completed, is right.
			sp := got[0][HdrLen:]
			if err := Complete(sp, mh); err != nil {
				t.Fatal(err)
			}
			validTCP(t, sp, l3)
		})
	}
}

// Two interleaved flows merge separately; a packet that can't merge (a
// FIN) ends its flow's run and keeps its place; other packets pass as is.
func TestCoalesceKeepsOrder(t *testing.T) {
	a, ha := superPacket(true, nil, 4*1388, 0, 1)
	b, hb := superPacket(true, nil, 2*1388, 0, 2)
	sa, sb := split(t, a, ha, 0), split(t, b, hb, 0)
	fin, hf := superPacket(true, nil, 100, 4*1388, 1)
	sf := split(t, fin, hf, 0)
	sf[0][HdrLen+33] |= tcpFIN
	after, ha2 := superPacket(true, nil, 1388, 4*1388+100, 1)
	s2 := split(t, after, ha2, 0)
	udp := make([]byte, HdrLen+28)
	udp[HdrLen], udp[HdrLen+9] = 0x45, 17

	c := Coalescer{}
	// a0 b0 a1 b1 a2 a3 udp fin(a) a-after
	for _, s := range [][]byte{sa[0], sb[0], sa[1], sb[1], sa[2], sa[3], udp, sf[0], s2[0]} {
		c.Add(s)
	}
	var lens []int
	c.Flush(func(p []byte) error { lens = append(lens, len(p)-HdrLen); return nil })
	want := []int{40 + 4*1388 + 12, 40 + 2*1388 + 12, 28, 40 + 12 + 100, 40 + 12 + 1388}
	if len(lens) != len(want) {
		t.Fatalf("lengths %v, want %v", lens, want)
	}
	for i := range want {
		if lens[i] != want[i] {
			t.Fatalf("lengths %v, want %v", lens, want)
		}
	}
}

func TestComplete(t *testing.T) {
	p, h := superPacket(true, nil, 500, 9, 9)
	h.GSOType = GSONone
	if err := Complete(p, h); err != nil {
		t.Fatal(err)
	}
	validTCP(t, p, 0)
}
