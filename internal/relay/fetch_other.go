//go:build !linux

package relay

import (
	"net"
	"net/netip"
	"syscall"
)

func originalDst(*net.TCPConn) (netip.AddrPort, bool) { return netip.AddrPort{}, false }

func markSocket(string, string, syscall.RawConn) error { return nil }
