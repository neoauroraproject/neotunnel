package tcptun

import (
	"net"

	"golang.org/x/sys/unix"
)

// useBBR switches a tunnel connection to BBR when the kernel has it: on a
// long path with some loss it keeps the rate where Cubic halves it.
func useBBR(c *net.TCPConn) { Tune(c, 0) }

// Tune switches c to BBR when the kernel has it and, with lowat > 0, sets
// TCP_NOTSENT_LOWAT: the socket then takes only that much data beyond what
// is in flight, so a relay's copy loop doesn't park megabytes in the send
// buffer (memory per connection, and latency for whatever comes next).
func Tune(c *net.TCPConn, lowat int) {
	rc, err := c.SyscallConn()
	if err != nil {
		return
	}
	rc.Control(func(fd uintptr) {
		unix.SetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION, "bbr")
		if lowat > 0 {
			unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, lowat)
		}
	})
}
