//go:build !linux

package mmsg

import (
	"errors"
	"net/netip"
	"syscall"
)

type Conn struct{}

func New(syscall.Conn, int) (*Conn, error) { return nil, errors.New("mmsg requires Linux") }

const SocketBuffer = 8 << 20

func SetBuffers(syscall.Conn, int) error { return errors.ErrUnsupported }

func (*Conn) EnableGRO() bool { return false }
func (*Conn) Segment(int) int { return 0 }

func (*Conn) Read([][]byte, []int, []netip.AddrPort) (int, error) { return 0, errors.ErrUnsupported }
func (*Conn) Write([][]byte, netip.AddrPort, bool) error          { return errors.ErrUnsupported }

func (*Conn) WriteGSO([][]byte, netip.AddrPort, bool) error { return errors.ErrUnsupported }
