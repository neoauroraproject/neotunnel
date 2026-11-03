package mmsg

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// SocketBuffer is the send and receive buffer size of a tunnel's socket. Big
// enough for the bursts of a fast tunnel, small enough not to add much delay
// when the server can't keep up: the kernel counts about twice the payload
// against it, so this is around 30 ms at 1 Gbit/s.
const SocketBuffer = 8 << 20

// SetBuffers gives a socket send and receive buffers of n bytes each. The
// plain options are cut down to net.core.rmem_max, about 200 KB until the
// network has been tuned: a burst of a tunnel's datagrams then overflows the
// socket, and each one lost makes the TCP inside slow down. The FORCE options
// ignore that limit (they need root, which the daemon has), so the plain ones
// are only the fallback.
func SetBuffers(sc syscall.Conn, n int) error {
	rc, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(func(fd uintptr) {
		for _, o := range [][2]int{{unix.SO_RCVBUFFORCE, unix.SO_RCVBUF}, {unix.SO_SNDBUFFORCE, unix.SO_SNDBUF}} {
			if unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, o[0], n) != nil {
				unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, o[1], n)
			}
		}
	})
}
