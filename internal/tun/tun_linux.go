// Package tun opens layer-3 TUN interfaces for the user-space data planes.
package tun

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// VnetHdrLen is the size of the virtio-net header in front of every packet
// on an interface opened with OpenVnet.
const VnetHdrLen = 10

// Open creates (or attaches to) a layer-3 TUN interface without packet
// information headers. The interface disappears when the file is closed.
func Open(name string) (*os.File, error) { return open(name, 0) }

// OpenVnet is Open with a virtio-net header in front of every packet read
// and written, so the interface can hand out and take super-packets (see
// SetOffload). Offloads start off: until then every packet read is an
// ordinary one with a zero header.
func OpenVnet(name string) (*os.File, error) { return open(name, unix.IFF_VNET_HDR) }

// OpenQueues is OpenVnet with n queues: the kernel spreads flows over them,
// so n readers (and writers) move packets in parallel. Fewer queues are
// opened when the interface allows fewer; it disappears when all are
// closed.
func OpenQueues(name string, n int) ([]*os.File, error) {
	var qs []*os.File
	for i := 0; i < max(n, 1); i++ {
		q, err := open(name, unix.IFF_VNET_HDR|unix.IFF_MULTI_QUEUE)
		if err != nil {
			if i > 0 {
				break
			}
			return nil, err
		}
		qs = append(qs, q)
	}
	return qs, nil
}

func open(name string, flags uint16) (*os.File, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w (is the tun module loaded?)", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI | flags)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	// Non-blocking so the Go poller serves reads and Close interrupts them.
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// SetOffload lets the kernel hand an OpenVnet interface TCP super-packets
// (up to 64 KiB, segmented later by whoever receives them) with checksums
// left to do, or makes it segment and checksum every packet itself again.
func SetOffload(f *os.File, on bool) error {
	var flags int
	if on {
		flags = unix.TUN_F_CSUM | unix.TUN_F_TSO4 | unix.TUN_F_TSO6 | unix.TUN_F_TSO_ECN
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	if err := rc.Control(func(fd uintptr) { ioErr = unix.IoctlSetInt(int(fd), unix.TUNSETOFFLOAD, flags) }); err != nil {
		return err
	}
	return ioErr
}
