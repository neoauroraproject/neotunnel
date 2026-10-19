package engine

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"neotunnel/internal/sysctl"
)

// Lanes: a tunnel on several lanes is N kernel tunnels between the same two
// servers, told apart by their GRE keys. Every lane holds this end's inner
// addresses as host addresses, and one multipath route reaches the far
// end's over all of them:
//
//	10.77.0.2 proto 77 src 10.77.0.1
//	    nexthop dev nt-x  nexthop dev nt-x.1  ...
//
// With the layer-4 multipath hash each inner flow stays on one lane while
// different flows spread over the lanes, and so over the CPU cores that
// receive and encrypt them. Forwards, return paths and health checks keep
// using the inner addresses, so they work unchanged.

// laneProto marks the routes NeoTunnel owns for lanes.
const laneProto = netlink.RouteProtocol(77)

// IPv6 refuses device-only next hops in a multipath route, so each lane
// also holds a fixed link-local address for its side (fe80::4e54:1 or :2)
// and IPv6 routes go via the other side's one on every lane.
func laneLinkLocal(side int) netip.Addr {
	return netip.AddrFrom16([16]byte{0: 0xfe, 1: 0x80, 12: 0x4e, 13: 0x54, 15: byte(side + 1)})
}

// laneGateway is the next hop toward far on a lane of s: far itself for
// IPv4 (on-link), the far side's lane link-local address for IPv6.
func laneGateway(s LinkSpec, far netip.Addr) netip.Addr {
	if far.Is4() {
		return far
	}
	return laneLinkLocal(1 - s.Side)
}

// addLaneLinkLocal puts this side's lane link-local address on a lane.
func addLaneLinkLocal(s LinkSpec) error {
	l, err := netlink.LinkByName(s.Name)
	if err != nil {
		return err
	}
	a := &netlink.Addr{IPNet: &net.IPNet{IP: toIP(laneLinkLocal(s.Side)), Mask: net.CIDRMask(64, 128)}, Flags: unix.IFA_F_NODAD}
	if err := netlink.AddrReplace(l, a); err != nil {
		return fmt.Errorf("link-local address on %s: %w", s.Name, err)
	}
	return nil
}

// lanesOf groups the specs of tunnels on several lanes by tunnel, lane 0
// first.
func lanesOf(specs []LinkSpec) (map[string][]LinkSpec, []string) {
	by := map[string][]LinkSpec{}
	var order []string
	for _, s := range specs {
		if s.Lanes < 2 {
			continue
		}
		if _, ok := by[s.Tunnel]; !ok {
			order = append(order, s.Tunnel)
		}
		by[s.Tunnel] = append(by[s.Tunnel], s)
	}
	return by, order
}

// laneHops is one next hop per lane that exists; gw (if valid) is the same
// on every lane: the far end's inner address on-link, or a link-local one.
func laneHops(lanes []LinkSpec, gw netip.Addr) []*netlink.NexthopInfo {
	var hops []*netlink.NexthopInfo
	for _, s := range lanes {
		l, err := netlink.LinkByName(s.Name)
		if err != nil {
			continue
		}
		h := &netlink.NexthopInfo{LinkIndex: l.Attrs().Index}
		if gw.IsValid() {
			h.Gw = toIP(gw)
			if !gw.IsLinkLocalUnicast() {
				h.Flags = int(netlink.FLAG_ONLINK)
			}
		}
		hops = append(hops, h)
	}
	return hops
}

// syncLanes installs the multipath routes of tunnels on several lanes and
// removes the ones no tunnel needs any more.
func syncLanes(specs []LinkSpec) []error {
	var errs []error
	by, order := lanesOf(specs)
	if len(order) > 0 {
		// Hash on ports too, so one server pair's flows use every lane.
		for _, k := range []string{"net.ipv4.fib_multipath_hash_policy", "net.ipv6.fib_multipath_hash_policy"} {
			if err := sysctl.Set(k, "1"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	wanted := map[string]bool{}
	for _, name := range order {
		lanes := by[name]
		lead := lanes[0]
		for i, far := range lead.Far {
			hops := laneHops(lanes, netip.Addr{})
			if far.Is6() {
				for _, s := range lanes {
					if err := addLaneLinkLocal(s); err != nil {
						errs = append(errs, fmt.Errorf("tunnel %s: %w", name, err))
					}
				}
				hops = laneHops(lanes, laneGateway(lead, far))
			}
			if len(hops) == 0 {
				continue
			}
			r := &netlink.Route{Dst: hostNet(far), MultiPath: hops, Protocol: laneProto}
			if far.Is4() && i < len(lead.Addrs) {
				// IPv6 picks the lane's address by itself (and a source
				// still in duplicate detection would be refused).
				r.Src = toIP(lead.Addrs[i].Addr())
			}
			if err := netlink.RouteReplace(r); err != nil {
				errs = append(errs, fmt.Errorf("tunnel %s: lane route to %s: %w", name, far, err))
				continue
			}
			wanted[r.Dst.String()] = true
		}
	}
	for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rs, err := netlink.RouteListFiltered(fam, &netlink.Route{Protocol: laneProto, Table: mainTable},
			netlink.RT_FILTER_PROTOCOL|netlink.RT_FILTER_TABLE)
		if err != nil {
			continue
		}
		for _, r := range rs {
			if r.Dst == nil || !wanted[r.Dst.String()] {
				netlink.RouteDel(&r)
			}
		}
	}
	return errs
}
