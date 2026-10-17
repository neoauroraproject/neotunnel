//go:build !linux

package relay

import "net"

func alive(*net.TCPConn) bool { return true }
