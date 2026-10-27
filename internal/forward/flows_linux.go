package forward

import (
	"fmt"
	"log"
	"syscall"

	"github.com/vishvananda/netlink"
)

// portFilter matches connections to any of the ports ("tcp/443").
type portFilter map[string]bool

func (f portFilter) MatchConntrackFlow(c *netlink.ConntrackFlow) bool {
	proto := map[uint8]string{syscall.IPPROTO_TCP: "tcp", syscall.IPPROTO_UDP: "udp"}[c.Forward.Protocol]
	return proto != "" && f[fmt.Sprintf("%s/%d", proto, c.Forward.DstPort)]
}

func forgetFlows(ports map[string]bool) {
	if len(ports) == 0 {
		return
	}
	var n uint
	for _, fam := range []netlink.InetFamily{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		if k, err := netlink.ConntrackDeleteFilters(netlink.ConntrackTable, fam, portFilter(ports)); err == nil {
			n += k
		}
	}
	if n > 0 {
		log.Printf("forwards changed: %d open connection(s) start over on the new path", n)
	}
}
