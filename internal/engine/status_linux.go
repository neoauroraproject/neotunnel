package engine

import (
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

// State reads an interface's live state and counters.
func State(name string) LinkState {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return LinkState{}
	}
	a := l.Attrs()
	s := LinkState{Exists: true, Up: a.Flags&net.FlagUp != 0, MTU: a.MTU}
	if st := a.Statistics; st != nil {
		s.RxBytes, s.TxBytes = st.RxBytes, st.TxBytes
		s.RxPkts, s.TxPkts = st.RxPackets, st.TxPackets
	}
	return s
}

// PublicIface is the interface of the default IPv4 route: the one whose
// traffic the datacenter counts.
func PublicIface() string {
	rs, err := netlink.RouteGet(net.IPv4(1, 1, 1, 1))
	if err != nil || len(rs) == 0 {
		return ""
	}
	l, err := netlink.LinkByIndex(rs[0].LinkIndex)
	if err != nil {
		return ""
	}
	return l.Attrs().Name
}

// PublicIPv6 is the global IPv6 address this host sends from on its default
// IPv6 route, or "" when it has none (many servers have IPv4 only).
func PublicIPv6() string {
	rs, err := netlink.RouteGet(net.ParseIP("2001:4860:4860::8888"))
	if err != nil || len(rs) == 0 || rs[0].Src == nil {
		return ""
	}
	if a, ok := netip.AddrFromSlice(rs[0].Src); ok && a.Is6() && a.IsGlobalUnicast() && !a.IsPrivate() {
		return a.String()
	}
	return ""
}
