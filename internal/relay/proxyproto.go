// Package relay serves port forwards in "proxy" mode: it accepts clients on
// this node's ports and relays them through the tunnel, telling the service
// on the far end who the client is with a PROXY protocol header (v1 or v2,
// the format HAProxy, nginx, Xray and others understand).
package relay

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

var v2sig = []byte("\r\n\r\n\x00\r\nQUIT\n")

// Header builds a PROXY protocol header for a connection from src to dst.
// udp selects DGRAM in v2 (v1 has no UDP form).
func Header(version int, udp bool, src, dst netip.AddrPort) []byte {
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	// Mixed families (an IPv4 client on an IPv6 socket, or the reverse) are
	// sent as IPv6 so both addresses fit one family.
	if src.Addr().Is4() != dst.Addr().Is4() {
		src = netip.AddrPortFrom(netip.AddrFrom16(src.Addr().As16()), src.Port())
		dst = netip.AddrPortFrom(netip.AddrFrom16(dst.Addr().As16()), dst.Port())
	}
	if version == 1 {
		fam := "TCP4"
		if src.Addr().Is6() {
			fam = "TCP6"
		}
		return []byte(fmt.Sprintf("PROXY %s %s %s %d %d\r\n", fam, src.Addr(), dst.Addr(), src.Port(), dst.Port()))
	}
	var fam byte = 0x11 // AF_INET, STREAM
	alen := 12
	if src.Addr().Is6() {
		fam, alen = 0x21, 36
	}
	if udp {
		fam++ // DGRAM
	}
	b := make([]byte, 0, 16+alen)
	b = append(b, v2sig...)
	b = append(b, 0x21, fam) // version 2, PROXY command
	b = binary.BigEndian.AppendUint16(b, uint16(alen))
	b = append(b, src.Addr().AsSlice()...)
	b = append(b, dst.Addr().AsSlice()...)
	b = binary.BigEndian.AppendUint16(b, src.Port())
	b = binary.BigEndian.AppendUint16(b, dst.Port())
	return b
}
