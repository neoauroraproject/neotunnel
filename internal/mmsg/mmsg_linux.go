// Package mmsg sends and receives batches of datagrams on a UDP or raw IP
// socket with one system call (sendmmsg, recvmmsg) instead of one each.
package mmsg

import (
	"errors"
	"net/netip"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type mmsghdr struct {
	hdr unix.Msghdr
	len uint32
	_   [4]byte
}

// Conn batches on one socket. Reads and writes each need their own Conn
// (they keep per-call state); several Conns can share a socket.
type Conn struct {
	rc    syscall.RawConn
	hdrs  []mmsghdr
	iovs  []unix.Iovec
	names []unix.RawSockaddrInet6 // big enough for either family
}

// New wraps a socket for batches of up to n datagrams.
func New(sc syscall.Conn, n int) (*Conn, error) {
	rc, err := sc.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &Conn{rc: rc, hdrs: make([]mmsghdr, n), iovs: make([]unix.Iovec, n), names: make([]unix.RawSockaddrInet6, n)}, nil
}

// Read receives up to len(bufs) datagrams, at least one, into bufs. For
// each it sets sizes[i] (-1 when the datagram didn't fit) and, when from
// is not nil, from[i].
func (c *Conn) Read(bufs [][]byte, sizes []int, from []netip.AddrPort) (int, error) {
	n := min(len(bufs), len(c.hdrs))
	for i := 0; i < n; i++ {
		c.iovs[i].Base = &bufs[i][0]
		c.iovs[i].SetLen(len(bufs[i]))
		h := &c.hdrs[i].hdr
		*h = unix.Msghdr{Iov: &c.iovs[i], Iovlen: 1}
		h.Name = (*byte)(unsafe.Pointer(&c.names[i]))
		h.Namelen = uint32(unsafe.Sizeof(c.names[i]))
	}
	var got int
	var errno syscall.Errno
	err := c.rc.Read(func(fd uintptr) bool {
		r, _, e := unix.Syscall6(unix.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&c.hdrs[0])), uintptr(n), 0, 0, 0)
		if e == unix.EAGAIN || e == unix.EINTR {
			return false
		}
		got, errno = int(r), e
		return true
	})
	if err != nil {
		return 0, err
	}
	if errno != 0 {
		return 0, errno
	}
	for i := 0; i < got; i++ {
		sizes[i] = int(c.hdrs[i].len)
		if c.hdrs[i].hdr.Flags&unix.MSG_TRUNC != 0 {
			sizes[i] = -1
		}
		if from != nil {
			from[i] = addrOf(&c.names[i])
		}
	}
	return got, nil
}

func addrOf(sa *unix.RawSockaddrInet6) netip.AddrPort {
	port := func(p uint16) uint16 { b := (*[2]byte)(unsafe.Pointer(&p)); return uint16(b[0])<<8 | uint16(b[1]) }
	switch sa.Family {
	case unix.AF_INET:
		s4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(sa))
		return netip.AddrPortFrom(netip.AddrFrom4(s4.Addr), port(s4.Port))
	case unix.AF_INET6:
		return netip.AddrPortFrom(netip.AddrFrom16(sa.Addr).Unmap(), port(sa.Port))
	}
	return netip.AddrPort{}
}

// put encodes to into sa for a socket of family v6 (IPv6 sockets take
// IPv4 addresses mapped), returning the length.
func put(sa *unix.RawSockaddrInet6, to netip.AddrPort, v6 bool) uint32 {
	*sa = unix.RawSockaddrInet6{}
	pb := (*[2]byte)(unsafe.Pointer(&sa.Port))
	if !v6 {
		s4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(sa))
		s4.Family = unix.AF_INET
		pb = (*[2]byte)(unsafe.Pointer(&s4.Port))
		pb[0], pb[1] = byte(to.Port()>>8), byte(to.Port())
		s4.Addr = to.Addr().As4()
		return unix.SizeofSockaddrInet4
	}
	sa.Family = unix.AF_INET6
	pb[0], pb[1] = byte(to.Port()>>8), byte(to.Port())
	sa.Addr = to.Addr().As16()
	return unix.SizeofSockaddrInet6
}

// maxGSO bounds one UDP GSO send: the kernel's segment limit and the
// largest datagram.
const (
	maxGSOSegs  = 64
	maxGSOBytes = 65000
)

// WriteGSO sends bufs, all the same size but the last (which may be
// shorter), as separate UDP datagrams with UDP GSO: each sendmsg takes up
// to 64 and the kernel cuts them apart only at the end of its path, so
// they cost about as much as one. An error means the kernel can't (no UDP
// GSO, or IPsec on the path); the caller falls back to Write.
func (c *Conn) WriteGSO(bufs [][]byte, to netip.AddrPort, v6 bool) error {
	size := len(bufs[0])
	var oob [unix.SizeofCmsghdr + 8]byte
	cm := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	cm.Level, cm.Type = unix.SOL_UDP, unix.UDP_SEGMENT
	cm.SetLen(unix.CmsgLen(2))
	*(*uint16)(unsafe.Pointer(&oob[unix.CmsgLen(0)])) = uint16(size)
	for len(bufs) > 0 {
		n, total := 0, 0
		for n < len(bufs) && n < len(c.iovs) && n < maxGSOSegs && total+len(bufs[n]) <= maxGSOBytes {
			c.iovs[n].Base = &bufs[n][0]
			c.iovs[n].SetLen(len(bufs[n]))
			total += len(bufs[n])
			n++
		}
		h := unix.Msghdr{Iov: &c.iovs[0], Iovlen: uint64(n), Control: &oob[0]}
		h.SetControllen(unix.CmsgSpace(2))
		if to.IsValid() {
			h.Name = (*byte)(unsafe.Pointer(&c.names[0]))
			h.Namelen = put(&c.names[0], to, v6)
		}
		var errno syscall.Errno
		err := c.rc.Write(func(fd uintptr) bool {
			_, _, e := unix.Syscall(unix.SYS_SENDMSG, fd, uintptr(unsafe.Pointer(&h)), 0)
			if e == unix.EAGAIN || e == unix.EINTR {
				return false
			}
			errno = e
			return true
		})
		if err != nil {
			return err
		}
		if errno != 0 {
			return errno
		}
		bufs = bufs[n:]
	}
	return nil
}

// Write sends every datagram in bufs to to (zero AddrPort: the socket is
// connected) on a socket of family v6. A datagram the kernel refuses (too
// large for the path, say) is skipped; the first such error is returned
// after the rest went out.
func (c *Conn) Write(bufs [][]byte, to netip.AddrPort, v6 bool) error {
	var first error
	for len(bufs) > 0 {
		n := min(len(bufs), len(c.hdrs))
		var namelen uint32
		if to.IsValid() {
			namelen = put(&c.names[0], to, v6)
		}
		for i := 0; i < n; i++ {
			c.iovs[i].Base = &bufs[i][0]
			c.iovs[i].SetLen(len(bufs[i]))
			h := &c.hdrs[i].hdr
			*h = unix.Msghdr{Iov: &c.iovs[i], Iovlen: 1}
			if namelen > 0 {
				h.Name = (*byte)(unsafe.Pointer(&c.names[0]))
				h.Namelen = namelen
			}
		}
		var sent int
		var errno syscall.Errno
		err := c.rc.Write(func(fd uintptr) bool {
			r, _, e := unix.Syscall6(unix.SYS_SENDMMSG, fd, uintptr(unsafe.Pointer(&c.hdrs[0])), uintptr(n), 0, 0, 0)
			if e == unix.EAGAIN || e == unix.EINTR {
				return false
			}
			sent, errno = int(r), e
			return true
		})
		if err != nil {
			return err
		}
		if errno != 0 {
			// The first datagram failed: skip it.
			if first == nil {
				first = errno
			}
			if errors.Is(errno, unix.EBADF) {
				return errno
			}
			sent = 1
		}
		bufs = bufs[sent:]
	}
	return first
}
