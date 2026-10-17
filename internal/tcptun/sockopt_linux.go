package tcptun

import (
	"net"

	"golang.org/x/sys/unix"
)

// useBBR switches a tunnel connection to BBR when the kernel has it: on a
// long path with some loss it keeps the rate where Cubic halves it.
func useBBR(c *net.TCPConn) {
	rc, err := c.SyscallConn()
	if err != nil {
		return
	}
	rc.Control(func(fd uintptr) {
		unix.SetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION, "bbr")
	})
}
