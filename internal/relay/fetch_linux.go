package relay

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

// soOriginalDst is SO_ORIGINAL_DST (IPv4) and IP6T_SO_ORIGINAL_DST (IPv6):
// where a redirected connection was going.
const soOriginalDst = 80

func originalDst(c *net.TCPConn) (netip.AddrPort, bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, false
	}
	var ap netip.AddrPort
	v4 := c.LocalAddr().(*net.TCPAddr).AddrPort().Addr().Unmap().Is4()
	raw.Control(func(fd uintptr) {
		if v4 {
			// A sockaddr_in in the first 16 bytes: family(2) port(2) addr(4).
			m, err := unix.GetsockoptIPv6Mreq(int(fd), unix.SOL_IP, soOriginalDst)
			if err == nil {
				a := m.Multiaddr
				ap = netip.AddrPortFrom(netip.AddrFrom4([4]byte(a[4:8])), binary.BigEndian.Uint16(a[2:4]))
			}
			return
		}
		i, err := unix.GetsockoptIPv6MTUInfo(int(fd), unix.SOL_IPV6, soOriginalDst)
		if err == nil {
			var p [2]byte
			binary.NativeEndian.PutUint16(p[:], i.Addr.Port) // stored in network order
			ap = netip.AddrPortFrom(netip.AddrFrom16(i.Addr.Addr).Unmap(), binary.BigEndian.Uint16(p[:]))
		}
	})
	return ap, ap.IsValid()
}

// markSocket sets FetchMark on a socket before it connects. Without
// CAP_NET_ADMIN (tests, a non-root run) there is no mark, and no redirect
// either, since installing one takes the same right: the socket goes as is.
func markSocket(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, FetchMark)
	})
	if err != nil {
		return err
	}
	if errors.Is(serr, unix.EPERM) {
		return nil
	}
	return serr
}
