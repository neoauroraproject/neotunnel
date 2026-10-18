package offload

import (
	"bytes"
	"encoding/binary"
)

// maxSuper is the most a super-packet holds from its IP header on.
const maxSuper = 65535

// Coalescer merges the consecutive TCP segments of each flow in one batch
// of received packets into super-packets, like the kernel's GRO: same
// headers apart from lengths, ids and checksums, sequence numbers that
// follow on, only ACK set (PSH ends a run), equal sizes but for the last.
// Everything else passes through unchanged and in order within its flow.
//
// Packets are added as b[:HdrLen] (room for the header, overwritten) and
// b[HdrLen:] (the packet); Flush hands every packet or super-packet, header
// in front, to write.
type Coalescer struct {
	// L3 is where the IP header starts: 0 on a TUN, 14 on a TAP (frames
	// whose IP header is elsewhere pass through).
	L3 int
	// Verify checks each segment's TCP checksum before merging, for
	// carriers that don't authenticate packets: a merged super-packet's
	// checksum is not checked again.
	Verify bool

	out   []entry
	flows map[flowKey]int // open run per flow, an index into out
	pool  [][]byte
}

type entry struct {
	b   []byte // a packet as added; or the super-packet being built
	run *run
}

type run struct {
	v4         bool
	ihl, thl   int
	mss, last  int // payload size of the first and the last segment
	next       uint32
	n          int // segments
	buf        []byte
	hdrsLength int // l3 + ihl + thl
}

type flowKey struct {
	addrs [32]byte
	ports [4]byte
	v4    bool
}

// tcpSeg is what merging needs to know about a packet.
type tcpSeg struct {
	v4       bool
	ihl, thl int
	seq      uint32
	plen     int
	flags    byte
}

func (c *Coalescer) parse(p []byte) (tcpSeg, bool) {
	var s tcpSeg
	l3 := c.L3
	if l3 > 0 && L3(p) != l3 {
		return s, false
	}
	if len(p) < l3+40 {
		return s, false
	}
	ip := p[l3:]
	switch ip[0] >> 4 {
	case 4:
		s.v4, s.ihl = true, 20
		if ip[0]&0x0f != 5 || ip[9] != 6 || int(binary.BigEndian.Uint16(ip[2:])) != len(ip) ||
			binary.BigEndian.Uint16(ip[6:])&0x3fff != 0 { // options, not TCP, padded, fragment
			return s, false
		}
	case 6:
		s.ihl = 40
		if len(ip) < 60 || ip[6] != 6 || int(binary.BigEndian.Uint16(ip[4:])) != len(ip)-40 {
			return s, false
		}
	default:
		return s, false
	}
	tcp := ip[s.ihl:]
	s.thl = int(tcp[12]>>4) * 4
	s.flags = tcp[13]
	if s.thl < 20 || s.thl > len(tcp) || s.flags&^tcpPSH != tcpACK {
		return s, false
	}
	s.plen = len(tcp) - s.thl
	if s.plen == 0 {
		return s, false
	}
	if c.Verify && fold(sum(tcp, pseudo(ip, s.v4, len(tcp)))) != 0xffff {
		return s, false
	}
	s.seq = binary.BigEndian.Uint32(tcp[4:])
	return s, true
}

func key(p []byte, l3 int, s tcpSeg) flowKey {
	k := flowKey{v4: s.v4}
	ip := p[l3:]
	if s.v4 {
		copy(k.addrs[:], ip[12:20])
	} else {
		copy(k.addrs[:], ip[8:40])
	}
	copy(k.ports[:], ip[s.ihl:s.ihl+4])
	return k
}

// Add queues one packet (see Coalescer).
func (c *Coalescer) Add(b []byte) {
	p := b[HdrLen:]
	s, ok := c.parse(p)
	if !ok {
		// Not mergeable: it ends the open run of its flow, if any, so the
		// flow's packets stay in order.
		if k, isTCP := c.flowOf(p); isTCP {
			delete(c.flows, k)
		}
		c.out = append(c.out, entry{b: b})
		return
	}
	if c.flows == nil {
		c.flows = map[flowKey]int{}
	}
	k := key(p, c.L3, s)
	if i, open := c.flows[k]; open && c.join(&c.out[i], p, s) {
		if s.flags&tcpPSH != 0 {
			delete(c.flows, k)
		}
		return
	}
	c.out = append(c.out, entry{b: b, run: &run{v4: s.v4, ihl: s.ihl, thl: s.thl, mss: s.plen, last: s.plen,
		next: s.seq + uint32(s.plen), n: 1, hdrsLength: c.L3 + s.ihl + s.thl}})
	if s.flags&tcpPSH == 0 {
		c.flows[k] = len(c.out) - 1
	} else {
		delete(c.flows, k)
	}
}

// flowOf finds the flow of a TCP packet that can't be merged.
func (c *Coalescer) flowOf(p []byte) (flowKey, bool) {
	l3 := c.L3
	if (l3 > 0 && L3(p) != l3) || len(p) < l3+40 || c.flows == nil {
		return flowKey{}, false
	}
	ip := p[l3:]
	s := tcpSeg{}
	switch {
	case ip[0]>>4 == 4 && ip[9] == 6:
		s.v4, s.ihl = true, int(ip[0]&0x0f)*4
	case ip[0]>>4 == 6 && ip[6] == 6 && len(ip) >= 44:
		s.ihl = 40
	default:
		return flowKey{}, false
	}
	if s.ihl+4 > len(ip) {
		return flowKey{}, false
	}
	return key(p, l3, s), true
}

// join appends segment p to the run in e when it continues it.
func (c *Coalescer) join(e *entry, p []byte, s tcpSeg) bool {
	r := e.run
	if r == nil || r.v4 != s.v4 || r.ihl != s.ihl || r.thl != s.thl || s.seq != r.next ||
		r.last != r.mss || s.plen > r.mss {
		return false
	}
	first := e.b[HdrLen:]
	if r.buf != nil {
		first = r.buf[HdrLen:]
	}
	if len(first)+s.plen > c.L3+maxSuper {
		return false
	}
	l3 := c.L3
	a, b := first[l3:], p[l3:]
	// Everything but lengths, ids and checksums must match; for TCP the
	// ack, data offset, window and options too, and PSH only on the new
	// segment.
	if !bytes.Equal(first[:l3], p[:l3]) {
		return false
	}
	if r.v4 {
		if a[1] != b[1] || !bytes.Equal(a[6:10], b[6:10]) || !bytes.Equal(a[12:20], b[12:20]) {
			return false
		}
	} else if !bytes.Equal(a[:4], b[:4]) || !bytes.Equal(a[6:40], b[6:40]) {
		return false
	}
	ta, tb := a[r.ihl:], b[r.ihl:]
	if !bytes.Equal(ta[8:13], tb[8:13]) || !bytes.Equal(ta[14:16], tb[14:16]) || !bytes.Equal(ta[20:r.thl], tb[20:r.thl]) {
		return false
	}
	if r.buf == nil {
		r.buf = c.buffer()[:len(e.b)]
		copy(r.buf, e.b)
	}
	r.buf = append(r.buf, p[r.hdrsLength:]...)
	r.buf[HdrLen+l3+r.ihl+13] |= s.flags & tcpPSH
	r.next += uint32(s.plen)
	r.last = s.plen
	r.n++
	return true
}

func (c *Coalescer) buffer() []byte {
	if n := len(c.pool); n > 0 {
		b := c.pool[n-1]
		c.pool = c.pool[:n-1]
		return b
	}
	return make([]byte, 0, HdrLen+c.L3+maxSuper)
}

// Flush writes everything added since the last Flush, in order, and stops
// at the first error.
func (c *Coalescer) Flush(write func([]byte) error) error {
	var err error
	for i := range c.out {
		e := &c.out[i]
		if err == nil {
			err = write(c.finish(e))
		}
		if e.run != nil && e.run.buf != nil {
			c.pool = append(c.pool, e.run.buf[:0])
		}
		*e = entry{}
	}
	c.out = c.out[:0]
	clear(c.flows)
	return err
}

// finish puts the header in front of an entry and, for a merged run, fixes
// up the super-packet's lengths and checksums.
func (c *Coalescer) finish(e *entry) []byte {
	r := e.run
	if r == nil || r.buf == nil {
		clear(e.b[:HdrLen])
		return e.b
	}
	b := r.buf
	ip := b[HdrLen+c.L3:]
	if r.v4 {
		binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)))
		ip[10], ip[11] = 0, 0
		binary.BigEndian.PutUint16(ip[10:], ^fold(sum(ip[:20], 0)))
	} else {
		binary.BigEndian.PutUint16(ip[4:], uint16(len(ip)-40))
	}
	tcp := ip[r.ihl:]
	// With the checksum left to do, the field holds the pseudo-header sum.
	binary.BigEndian.PutUint16(tcp[16:], fold(pseudo(ip, r.v4, len(tcp))))
	h := Hdr{Flags: FlagNeedsCsum, GSOType: GSOTCPv6, HdrLen: uint16(r.hdrsLength), GSOSize: uint16(r.mss),
		CsumStart: uint16(c.L3 + r.ihl), CsumOffset: 16}
	if r.v4 {
		h.GSOType = GSOTCPv4
	}
	h.Put(b)
	return b
}
