package engine

import (
	"fmt"
	"net/netip"

	"github.com/vishvananda/netlink"

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

// laneHops is one next hop per lane that exists; gw (if valid) is the far
// end's inner address, on-link on every lane.
func laneHops(lanes []LinkSpec, gw netip.Addr) []*netlink.NexthopInfo {
	var hops []*netlink.NexthopInfo
	for _, s := range lanes {
		l, err := netlink.LinkByName(s.Name)
		if err != nil {
			continue
		}
		h := &netlink.NexthopInfo{LinkIndex: l.Attrs().Index}
		if gw.IsValid() {
			h.Gw, h.Flags = toIP(gw), int(netlink.FLAG_ONLINK)
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
		hops := laneHops(lanes, netip.Addr{})
		if len(hops) == 0 {
			continue
		}
		lead := lanes[0]
		for i, far := range lead.Far {
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
