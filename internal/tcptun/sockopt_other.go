//go:build !linux

package tcptun

import "net"

func useBBR(*net.TCPConn) {}

// Tune is a no-op outside Linux.
func Tune(*net.TCPConn, int) {}
