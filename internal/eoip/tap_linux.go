package eoip

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"neotunnel/internal/tun"
)

// openTAP creates (or attaches to) a layer-2 TAP interface without packet
// information headers and gives it mac. With a virtio-net header and
// offload when the kernel has them (vnet), so it hands out and takes TCP
// super-frames (see package offload). The interface disappears when the
// file is closed.
func openTAP(name string, mac net.HardwareAddr) (*os.File, bool, error) {
	if f, err := openTAPFlags(name, mac, unix.IFF_VNET_HDR); err == nil {
		if tun.SetOffload(f, true) == nil {
			return f, true, nil
		}
		f.Close()
	}
	f, err := openTAPFlags(name, mac, 0)
	return f, false, err
}

func openTAPFlags(name string, mac net.HardwareAddr, flags uint16) (*os.File, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w (is the tun module loaded?)", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	ifr.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI | flags)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	if l, err := netlink.LinkByName(name); err == nil && l.Attrs().HardwareAddr.String() != mac.String() {
		if err := netlink.LinkSetHardwareAddr(l, mac); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("set MAC of %s: %w", name, err)
		}
	}
	// Non-blocking so the Go poller serves reads and Close interrupts them.
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// tune sets the outer packets' TTL, DSCP and Don't-Fragment behavior.
func tune(car carrier, c Config) error {
	var sc syscall.Conn
	switch k := car.(type) {
	case *rawCarrier:
		sc = k.conn
	case *udpCarrier:
		sc = k.conn
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	ttl := int(c.TTL)
	if ttl == 0 {
		ttl = 255
	}
	tos := int(c.DSCP) << 2
	var serr error
	set := func(fd uintptr, level, opt, v int) {
		if e := unix.SetsockoptInt(int(fd), level, opt, v); e != nil && serr == nil {
			serr = fmt.Errorf("socket option %d/%d: %w", level, opt, e)
		}
	}
	// The PMTUDISC values are the same for both families.
	pmtu := unix.IP_PMTUDISC_DO
	if c.NoDF {
		pmtu = unix.IP_PMTUDISC_DONT
	}
	err = rc.Control(func(fd uintptr) {
		if c.Remote.Is6() {
			set(fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, ttl)
			set(fd, unix.IPPROTO_IPV6, unix.IPV6_TCLASS, tos)
			set(fd, unix.IPPROTO_IPV6, unix.IPV6_MTU_DISCOVER, pmtu)
		} else {
			set(fd, unix.IPPROTO_IP, unix.IP_TTL, ttl)
			set(fd, unix.IPPROTO_IP, unix.IP_TOS, tos)
			set(fd, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, pmtu)
		}
		unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 4<<20)
		unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, 4<<20)
	})
	if err != nil {
		return err
	}
	return serr
}
