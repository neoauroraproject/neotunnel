package probe

import (
	"net"

	"golang.org/x/sys/unix"
)

// acked is how many bytes the far end has acknowledged on c: what really
// arrived, unlike what was written, which the first moments of an upload
// overstate by everything that fits in the socket buffers.
func acked(c net.Conn) (uint64, bool) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return 0, false
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var n uint64
	var ierr error
	if err := raw.Control(func(fd uintptr) {
		var info *unix.TCPInfo
		info, ierr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
		if ierr == nil {
			n = info.Bytes_acked
		}
	}); err != nil || ierr != nil {
		return 0, false
	}
	return n, true
}
