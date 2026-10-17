package relay

import (
	"net"

	"golang.org/x/sys/unix"
)

// alive reports whether an idle pooled connection is still open: the exit
// sends nothing until it is used, so any byte, end of stream or error means
// it is gone.
func alive(c *net.TCPConn) bool {
	rc, err := c.SyscallConn()
	if err != nil {
		return false
	}
	ok := false
	rc.Read(func(fd uintptr) bool {
		var b [1]byte
		_, _, err := unix.Recvfrom(int(fd), b[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
		ok = err == unix.EAGAIN
		return true
	})
	return ok
}
