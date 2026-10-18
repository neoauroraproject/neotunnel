// Package offload lets the user-space tunnels move TCP super-packets
// instead of single packets, with nothing changed on the wire.
//
// A TUN or TAP interface opened with a virtio-net header (and offload on)
// hands out TCP super-packets of up to 64 KiB, with checksums left to do;
// Segment cuts one into the packets the far end expects, so one read
// replaces dozens. The other way, Coalescer merges the consecutive TCP
// segments of a flow in a batch of received packets back into
// super-packets, so one write to the interface replaces dozens. Both match
// what the kernel's own GSO and GRO do.
package offload

import (
	"encoding/binary"
	"errors"
)

// The virtio-net header (struct virtio_net_hdr, little-endian).
const (
	HdrLen = 10

	FlagNeedsCsum = 1

	GSONone  = 0
	GSOTCPv4 = 1
	GSOTCPv6 = 4
	GSOECN   = 0x80
)

// Hdr is a parsed virtio-net header.
type Hdr struct {
	Flags, GSOType                         uint8
	HdrLen, GSOSize, CsumStart, CsumOffset uint16
}

// ParseHdr reads the header at the start of b.
func ParseHdr(b []byte) Hdr {
	return Hdr{
		Flags: b[0], GSOType: b[1],
		HdrLen:     binary.LittleEndian.Uint16(b[2:]),
		GSOSize:    binary.LittleEndian.Uint16(b[4:]),
		CsumStart:  binary.LittleEndian.Uint16(b[6:]),
		CsumOffset: binary.LittleEndian.Uint16(b[8:]),
	}
}

// Put writes h at the start of b.
func (h Hdr) Put(b []byte) {
	b[0], b[1] = h.Flags, h.GSOType
	binary.LittleEndian.PutUint16(b[2:], h.HdrLen)
	binary.LittleEndian.PutUint16(b[4:], h.GSOSize)
	binary.LittleEndian.PutUint16(b[6:], h.CsumStart)
	binary.LittleEndian.PutUint16(b[8:], h.CsumOffset)
}

var errPacket = errors.New("offload: malformed packet")

// sum adds b to s as big-endian 16-bit words (ones' complement, unfolded).
func sum(b []byte, s uint64) uint64 {
	for len(b) >= 8 {
		s += uint64(binary.BigEndian.Uint32(b)) + uint64(binary.BigEndian.Uint32(b[4:]))
		b = b[8:]
	}
	for len(b) >= 2 {
		s += uint64(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		s += uint64(b[0]) << 8
	}
	return s
}

func fold(s uint64) uint16 {
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return uint16(s)
}

// pseudo is the TCP pseudo-header sum of the IP packet at ip for a TCP
// segment of n bytes.
func pseudo(ip []byte, v4 bool, n int) uint64 {
	if v4 {
		return sum(ip[12:20], 6+uint64(n))
	}
	return sum(ip[8:40], 6+uint64(n))
}

// L3 is where the IP header starts in an Ethernet frame (after one VLAN
// tag at most), or -1 when the frame carries no IP.
func L3(frame []byte) int {
	if len(frame) < 14 {
		return -1
	}
	off, et := 12, binary.BigEndian.Uint16(frame[12:])
	if et == 0x8100 || et == 0x88a8 {
		if len(frame) < 18 {
			return -1
		}
		off, et = 16, binary.BigEndian.Uint16(frame[16:])
	}
	if et != 0x0800 && et != 0x86dd {
		return -1
	}
	return off + 2
}

// Complete fills in a checksum the kernel left to do (FlagNeedsCsum): the
// field at CsumStart+CsumOffset holds the pseudo-header sum.
func Complete(pkt []byte, h Hdr) error {
	if h.Flags&FlagNeedsCsum == 0 {
		return nil
	}
	start, at := int(h.CsumStart), int(h.CsumStart)+int(h.CsumOffset)
	if start > len(pkt) || at+2 > len(pkt) {
		return errPacket
	}
	c := ^fold(sum(pkt[start:], 0))
	if c == 0 && h.CsumOffset == 6 { // UDP sends 0 as all ones
		c = 0xffff
	}
	binary.BigEndian.PutUint16(pkt[at:], c)
	return nil
}

// Segment cuts the TCP super-packet pkt (IP header at l3) into packets of
// at most GSOSize payload bytes, written to out[i][off:] with their length
// in sizes[i]; it returns how many. Each gets its own IP length, IPv4 id
// and checksum, sequence number and TCP checksum; FIN and PSH stay on the
// last, CWR on the first.
func Segment(pkt []byte, h Hdr, l3 int, out [][]byte, off int, sizes []int) (int, error) {
	gt := h.GSOType &^ GSOECN
	if gt != GSOTCPv4 && gt != GSOTCPv6 {
		return 0, errors.New("offload: only TCP super-packets are split")
	}
	cs, mss := int(h.CsumStart), int(h.GSOSize)
	if l3 < 0 || l3+40 > len(pkt) || cs+20 > len(pkt) || mss == 0 {
		return 0, errPacket
	}
	v4 := pkt[l3]>>4 == 4
	hl := cs + int(pkt[cs+12]>>4)*4
	if hl > len(pkt) || hl <= cs {
		return 0, errPacket
	}
	payload := pkt[hl:]
	n := (len(payload) + mss - 1) / mss
	if n == 0 {
		n = 1
	}
	if n > len(out) || n > len(sizes) {
		return 0, errors.New("offload: too many segments")
	}
	seq := binary.BigEndian.Uint32(pkt[cs+4:])
	id := binary.BigEndian.Uint16(pkt[l3+4:])
	flags := pkt[cs+13]
	for i := 0; i < n; i++ {
		from, to := i*mss, min((i+1)*mss, len(payload))
		b := out[i][off:]
		if len(b) < hl+to-from {
			return 0, errors.New("offload: segment buffer too small")
		}
		m := copy(b, pkt[:hl])
		m += copy(b[m:], payload[from:to])
		s := b[:m]
		ip := s[l3:]
		if v4 {
			ihl := int(ip[0]&0x0f) * 4
			binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)))
			binary.BigEndian.PutUint16(ip[4:], id+uint16(i))
			ip[10], ip[11] = 0, 0
			binary.BigEndian.PutUint16(ip[10:], ^fold(sum(ip[:ihl], 0)))
		} else {
			binary.BigEndian.PutUint16(ip[4:], uint16(len(ip)-40))
		}
		f := flags
		if i != n-1 {
			f &^= tcpFIN | tcpPSH
		}
		if i != 0 {
			f &^= tcpCWR
		}
		s[cs+13] = f
		binary.BigEndian.PutUint32(s[cs+4:], seq+uint32(from))
		s[cs+16], s[cs+17] = 0, 0
		binary.BigEndian.PutUint16(s[cs+16:], ^fold(sum(s[cs:], pseudo(ip, v4, m-cs))))
		sizes[i] = m
	}
	return n, nil
}

const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpPSH = 0x08
	tcpACK = 0x10
	tcpURG = 0x20
	tcpECE = 0x40
	tcpCWR = 0x80
)

// Segments is how many packets Segment makes of pkt and the size of the
// largest, for sizing buffers (0, 0 when pkt is not a TCP super-packet).
func Segments(pkt []byte, h Hdr) (n, size int) {
	cs, mss := int(h.CsumStart), int(h.GSOSize)
	if cs+20 > len(pkt) || mss == 0 {
		return 0, 0
	}
	hl := cs + int(pkt[cs+12]>>4)*4
	if hl > len(pkt) {
		return 0, 0
	}
	return max(1, (len(pkt)-hl+mss-1)/mss), hl + mss
}
