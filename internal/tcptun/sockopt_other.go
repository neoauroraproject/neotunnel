//go:build !linux

package tcptun

import "net"

func useBBR(*net.TCPConn) {}
