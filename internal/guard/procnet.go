package guard

import (
	"bufio"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
)

// ProcSocket is one line of /proc/net/{tcp,tcp6,udp,udp6}.
type ProcSocket struct {
	Local netip.AddrPort
	State int // 0x0A: TCP listen; 0x07: UDP unconnected; 0x01: TCP established
	Inode uint64
}

// TCP and UDP states in /proc/net.
const (
	StateEstablished = 0x01
	StateUDPOpen     = 0x07
	StateListen      = 0x0A
)

// ParseProcNet reads a /proc/net socket table. Addresses are hex in host
// byte order per 32-bit word (little endian on the machines NeoTunnel runs
// on), ports big endian.
func ParseProcNet(text string) []ProcSocket {
	var out []ProcSocket
	sc := bufio.NewScanner(strings.NewReader(text))
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		local, ok := parseHexAddrPort(f[1])
		if !ok {
			continue
		}
		st, err := strconv.ParseUint(f[3], 16, 8)
		if err != nil {
			continue
		}
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		out = append(out, ProcSocket{Local: local, State: int(st), Inode: inode})
	}
	return out
}

func parseHexAddrPort(s string) (netip.AddrPort, bool) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	// Each 32-bit word is in host (little endian) order.
	for i := 0; i+4 <= len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	a, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(a.Unmap(), uint16(port)), true
}

// Reachable reports whether a socket bound to a can be reached from
// outside: not the loopback.
func Reachable(a netip.Addr) bool { return !a.IsLoopback() }
