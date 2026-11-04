package engine

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/forward"
)

// Forwards to the far server's public address (config.Forward.Public): that
// address is also where the tunnel sends its own outer packets, so it isn't
// routed through the tunnel as such. The nft table marks the forwards'
// connections instead, and one rule per mark sends marked packets to the
// tunnel's table, which holds a single route: the far server's public
// address via its address inside the tunnel.
//
//	7695  fwmark P lookup P          P: forward.PublicMark(tunnel)
//	table P: <far public> via <far inner> dev <tunnel>
const publicRulePriority = 7695

func syncPublic(ps []forward.Public, specs []LinkSpec) []error {
	var errs []error
	lanes, _ := lanesOf(specs)
	tunnelOf := map[string]string{}
	for _, s := range specs {
		tunnelOf[s.Name] = s.Tunnel
	}
	type want struct {
		mark uint32
		fam  int
	}
	wanted := map[want]bool{}
	for _, p := range ps {
		l, err := netlink.LinkByName(p.Iface)
		if err != nil {
			continue
		}
		fam, bits := netlink.FAMILY_V4, 32
		if p.Dest.Is6() {
			fam, bits = netlink.FAMILY_V6, 128
		}
		dst := &net.IPNet{IP: p.Dest.AsSlice(), Mask: net.CIDRMask(bits, bits)}
		route := &netlink.Route{LinkIndex: l.Attrs().Index, Dst: dst, Gw: toIP(p.Via), Table: int(p.Mark)}
		if hs := lanes[tunnelOf[p.Iface]]; len(hs) > 1 {
			// Spread over the lanes like the tunnel's other traffic.
			route = &netlink.Route{Dst: dst, Table: int(p.Mark), MultiPath: laneHops(hs, laneGateway(hs[0], p.Via))}
		}
		if err := netlink.RouteReplace(route); err != nil {
			errs = append(errs, fmt.Errorf("route to %s through %s: %w", p.Dest, p.Iface, err))
			continue
		}
		wanted[want{p.Mark, fam}] = true
	}
	for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := netlink.RuleList(fam)
		if err != nil {
			continue
		}
		have := map[uint32]bool{}
		for _, r := range rules {
			if r.Priority != publicRulePriority || !forward.IsPublicMark(r.Mark) {
				continue
			}
			if !wanted[want{r.Mark, fam}] {
				netlink.RuleDel(&r)
				flushTable(fam, r.Table)
				continue
			}
			have[r.Mark] = true
		}
		for w := range wanted {
			if w.fam == fam && !have[w.mark] {
				if err := netlink.RuleAdd(markRule(fam, publicRulePriority, int(w.mark), w.mark)); err != nil {
					errs = append(errs, fmt.Errorf("public address rule: %w", err))
				}
			}
		}
	}
	return errs
}
