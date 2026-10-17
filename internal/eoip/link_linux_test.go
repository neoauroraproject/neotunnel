package eoip

import (
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

func rxPackets(name string) int {
	b, _ := os.ReadFile("/sys/class/net/" + name + "/statistics/rx_packets")
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// TestLoopbackFrames runs links over loopback on a real kernel (as root):
// frames sent out of one TAP arrive on the TAP with the same tunnel id, and
// a link with another id turns them away.
func TestLoopbackFrames(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skip("no /dev/net/tun")
	}
	for _, lo := range []string{"127.0.0.1", "::1"} {
		addr := netip.MustParseAddr(lo)
		start := func(name string, side int, id uint16) *Link {
			l, err := Start(Config{Name: name, Tunnel: "lo", Side: side, ID: id, Local: addr, Remote: addr})
			if err != nil {
				t.Fatalf("%s over %s: %v", name, lo, err)
			}
			nl, _ := netlink.LinkByName(name)
			netlink.LinkSetUp(nl)
			return l
		}
		a, b, other := start("eoipt-a", 0, 77), start("eoipt-b", 1, 77), start("eoipt-c", 1, 78)
		nl, _ := netlink.LinkByName("eoipt-a")
		netlink.AddrAdd(nl, &netlink.Addr{IPNet: &net.IPNet{IP: net.IPv4(10, 250, 0, 1), Mask: net.CIDRMask(24, 32)}})
		// A datagram to an unknown neighbor sends ARP requests out of a.
		if c, err := net.Dial("udp4", "10.250.0.2:9"); err == nil {
			c.Write([]byte("x"))
			c.Close()
		}
		ok := false
		for i := 0; i < 40 && !ok; i++ {
			time.Sleep(50 * time.Millisecond)
			ok = rxPackets("eoipt-b") > 0
		}
		if !ok {
			t.Errorf("over %s: no frame reached the far TAP", lo)
		}
		// On loopback c also hears itself, so check that it turned the
		// others' packets away rather than that its TAP stayed silent.
		if other.BadPackets.Load() == 0 {
			t.Errorf("over %s: a link with another tunnel id took the others' packets", lo)
		}
		if a.cfg.framing() != map[bool]framing{true: framingEtherIP, false: framingGRE}[addr.Is6()] {
			t.Errorf("over %s: wrong framing", lo)
		}
		a.Close()
		b.Close()
		other.Close()
	}
}
