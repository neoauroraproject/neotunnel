//go:build !linux

package mmsg

import (
	"errors"
	"net/netip"
	"syscall"
)

type Conn struct{}

func New(syscall.Conn, int) (*Conn, error) { return nil, errors.New("mmsg requires Linux") }

func (*Conn) Read([][]byte, []int, []netip.AddrPort) (int, error) { return 0, errors.ErrUnsupported }
func (*Conn) Write([][]byte, netip.AddrPort, bool) error          { return errors.ErrUnsupported }

func (*Conn) WriteGSO([][]byte, netip.AddrPort, bool) error { return errors.ErrUnsupported }
