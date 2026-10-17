// Package eoip is MikroTik's Ethernet over IP in user space: Ethernet
// frames from a TAP interface travel to the far end in EoIP's own framing,
// so two NeoTunnel servers (or a NeoTunnel server and a MikroTik) share a
// layer-2 segment without any kernel module beyond tun.
//
// EoIP (IPv4, IP protocol 47) is a GRE version 1 packet:
//
//	20 01 | 64 00 | length (BE) | tunnel id (LE) | Ethernet frame
//
// EoIPv6 (IPv6, IP protocol 97) is an EtherIP header (RFC 3378) whose
// reserved bits hold a 12-bit tunnel id, with the version nibble and the
// id's top nibble swapped as MikroTik sends them:
//
//	id[11:8]<<4 | 3 | id[7:0] | Ethernet frame
//
// On a UDP port the EoIP (GRE) header rides inside UDP over either family,
// like FOU does for kernel GRE.
package eoip

import (
	"encoding/binary"
	"errors"
)

const (
	// GREHeaderLen is the size of EoIP's GRE header.
	GREHeaderLen = 8
	// EtherIPHeaderLen is the size of EoIPv6's EtherIP header.
	EtherIPHeaderLen = 2

	greFlags     = 0x2001 // key present, version 1
	greProto     = 0x6400 // MikroTik EoIP
	etherIPVer   = 0x03
	maxEtherIPID = 0x0fff
)

var errPacket = errors.New("not an EoIP packet")

// framing is one of the two EoIP headers.
type framing int

const (
	framingGRE     framing = iota // EoIP: IPv4, or UDP over either family
	framingEtherIP                // EoIPv6 over raw IPv6
)

func (f framing) headerLen() int {
	if f == framingEtherIP {
		return EtherIPHeaderLen
	}
	return GREHeaderLen
}

// put writes the header for a frame of n bytes into b[:headerLen()].
func (f framing) put(b []byte, id uint16, n int) {
	if f == framingEtherIP {
		b[0] = byte(id>>8&0x0f)<<4 | etherIPVer
		b[1] = byte(id)
		return
	}
	binary.BigEndian.PutUint16(b[0:], greFlags)
	binary.BigEndian.PutUint16(b[2:], greProto)
	binary.BigEndian.PutUint16(b[4:], uint16(n))
	binary.LittleEndian.PutUint16(b[6:], id)
}

// parse returns the tunnel id and the frame a packet carries. An empty
// frame is a keepalive.
func (f framing) parse(p []byte) (uint16, []byte, error) {
	if f == framingEtherIP {
		if len(p) < EtherIPHeaderLen || p[0]&0x0f != etherIPVer {
			return 0, nil, errPacket
		}
		return uint16(p[0]>>4)<<8 | uint16(p[1]), p[EtherIPHeaderLen:], nil
	}
	if len(p) < GREHeaderLen || binary.BigEndian.Uint16(p) != greFlags || binary.BigEndian.Uint16(p[2:]) != greProto {
		return 0, nil, errPacket
	}
	n := int(binary.BigEndian.Uint16(p[4:]))
	frame := p[GREHeaderLen:]
	if n > len(frame) {
		return 0, nil, errPacket
	}
	return binary.LittleEndian.Uint16(p[6:]), frame[:n], nil
}
