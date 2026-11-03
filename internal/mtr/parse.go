package mtr

import "encoding/binary"

// parse reads an ICMP packet as a raw socket gives it (IPv4: with its IP
// header; IPv6: without) and finds the probe it answers: an echo reply of
// ours, or a time exceeded or unreachable message that quotes one.
func parse(b []byte, v6 bool, id uint16) (seq uint16, echo, ok bool) {
	if !v6 {
		if len(b) < 20 {
			return 0, false, false
		}
		ihl := int(b[0]&0x0f) * 4
		if len(b) < ihl+8 {
			return 0, false, false
		}
		b = b[ihl:]
	}
	if len(b) < 8 {
		return 0, false, false
	}
	typ := b[0]
	reply, exceeded, unreach := byte(0), byte(11), byte(3)
	if v6 {
		reply, exceeded, unreach = 129, 3, 1
	}
	switch typ {
	case reply:
		if binary.BigEndian.Uint16(b[4:]) != id {
			return 0, false, false
		}
		return binary.BigEndian.Uint16(b[6:]), true, true
	case exceeded, unreach:
		q := b[8:] // the quoted packet: its IP header, then our echo header
		if v6 {
			if len(q) < 40+8 || q[6] != 58 { // next header: ICMPv6
				return 0, false, false
			}
			q = q[40:]
		} else {
			if len(q) < 20 {
				return 0, false, false
			}
			ihl := int(q[0]&0x0f) * 4
			if len(q) < ihl+8 || q[9] != 1 { // protocol: ICMP
				return 0, false, false
			}
			q = q[ihl:]
		}
		if binary.BigEndian.Uint16(q[4:]) != id {
			return 0, false, false
		}
		// A target that answers "unreachable" is reached too.
		return binary.BigEndian.Uint16(q[6:]), typ == unreach, true
	}
	return 0, false, false
}
