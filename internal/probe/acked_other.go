//go:build !linux

package probe

import "net"

func acked(net.Conn) (uint64, bool) { return 0, false }
