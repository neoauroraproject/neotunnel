package relay

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// markControl sets the routing mark (SO_MARK) on a dialer's socket, nil
// for no mark.
func markControl(mark uint32) func(network, address string, c syscall.RawConn) error {
	if mark == 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
		}); err != nil {
			return err
		}
		return serr
	}
}
