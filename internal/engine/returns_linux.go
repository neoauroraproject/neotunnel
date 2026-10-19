package engine

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
	"neotunnel/internal/sysctl"
)

// Return paths for transparent forwards: the far side DNATs clients into the
// tunnel without hiding their addresses, so replies here would follow the
// default route instead of the tunnel. Connections that came in through the
// tunnel are marked (by the nft table; TCP sockets inherit the mark of their
// SYN), and two routing rules per mark do what wg-quick does:
//
//	7698  fwmark M to <far server> lookup main          the tunnel's own packets
//	7699  fwmark M lookup main suppress_prefixlength 0   specific routes win
//	7700  fwmark M lookup M                             else: the tunnel
//
// so a container network or a LAN behind this server keeps working. 7698
// matters for tunnels in UDP (GENEVE, VXLAN): the kernel copies a reply's
// mark onto the outer packet it wraps it in, and without the rule that
// packet, bound for the far server, would be routed back into the tunnel.

const (
	returnRulePriority = 7700
	mainRulePriority   = 7699
	outerRulePriority  = 7698
	mainTable          = 254
)

func ours(mark uint32) bool { return mark&0xffff0000 == 0x4e540000 }

func syncReturns(rs []forward.Return, specs []LinkSpec) []error {
	var errs []error
	if len(rs) > 0 {
		if err := sysctl.Set("net.ipv4.tcp_fwmark_accept", "1"); err != nil {
			errs = append(errs, err)
		}
	}
	byIface := map[string]LinkSpec{}
	for _, s := range specs {
		byIface[s.Name] = s
	}
	type want struct {
		mark uint32
		fam  int
	}
	lanes, _ := lanesOf(specs)
	wanted := map[want]bool{}
	// The far servers each mark's tunnel sends its outer packets to.
	type outer struct {
		mark uint32
		to   netip.Addr
	}
	outers := map[outer]bool{}
	bottom := func(s LinkSpec) LinkSpec {
		for i := 0; i < 8 && s.Parent != ""; i++ {
			p, ok := byIface[s.Parent]
			if !ok {
				break
			}
			s = p
		}
		return s
	}
	for _, r := range rs {
		s, ok := byIface[r.Iface]
		if !ok {
			continue
		}
		l, err := netlink.LinkByName(s.Name)
		if err != nil {
			continue
		}
		for i, a := range s.Addrs {
			peer, ok := config.Inner(a.Masked().String(), 1-s.Side)
			if s.Lanes > 1 && i < len(s.Far) {
				peer, ok = netip.PrefixFrom(s.Far[i], s.Far[i].BitLen()), true
			}
			if !ok {
				continue
			}
			fam, dst := netlink.FAMILY_V4, &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}
			if a.Addr().Is6() {
				fam, dst = netlink.FAMILY_V6, &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}
			}
			route := &netlink.Route{LinkIndex: l.Attrs().Index, Dst: dst, Gw: toIP(peer.Addr()), Table: int(r.Mark)}
			if s.Lanes > 1 {
				// Replies spread over the lanes like any other traffic.
				route = &netlink.Route{Dst: dst, Table: int(r.Mark), MultiPath: laneHops(lanes[s.Tunnel], laneGateway(s, peer.Addr()))}
			}
			if err := netlink.RouteReplace(route); err != nil {
				errs = append(errs, fmt.Errorf("return route via %s: %w", s.Name, err))
				continue
			}
			wanted[want{r.Mark, fam}] = true
		}
		if b := bottom(s); b.Remote.IsValid() {
			outers[outer{r.Mark, b.Remote.Unmap()}] = true
		}
	}
	for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := netlink.RuleList(fam)
		if err != nil {
			continue
		}
		have := map[int]map[uint32]bool{mainRulePriority: {}, returnRulePriority: {}}
		haveOuter := map[outer]bool{}
		for _, r := range rules {
			if r.Priority == outerRulePriority && ours(r.Mark) && r.Dst != nil {
				a, _ := netip.AddrFromSlice(r.Dst.IP)
				o := outer{r.Mark, a.Unmap()}
				if outers[o] {
					haveOuter[o] = true
				} else {
					netlink.RuleDel(&r)
				}
				continue
			}
			if (r.Priority != returnRulePriority && r.Priority != mainRulePriority) || !ours(r.Mark) {
				continue
			}
			if !wanted[want{r.Mark, fam}] {
				netlink.RuleDel(&r)
				if r.Priority == returnRulePriority {
					flushTable(fam, r.Table)
				}
				continue
			}
			have[r.Priority][r.Mark] = true
		}
		for o := range outers {
			if haveOuter[o] || o.to.Is4() != (fam == netlink.FAMILY_V4) {
				continue
			}
			r := markRule(fam, outerRulePriority, mainTable, o.mark)
			r.Dst = &net.IPNet{IP: o.to.AsSlice(), Mask: net.CIDRMask(o.to.BitLen(), o.to.BitLen())}
			if err := netlink.RuleAdd(r); err != nil {
				errs = append(errs, fmt.Errorf("return path rule: %w", err))
			}
		}
		for w := range wanted {
			if w.fam != fam {
				continue
			}
			if !have[mainRulePriority][w.mark] {
				r := markRule(fam, mainRulePriority, mainTable, w.mark)
				r.SuppressPrefixlen = 0
				if err := netlink.RuleAdd(r); err != nil {
					errs = append(errs, fmt.Errorf("return path rule: %w", err))
				}
			}
			if !have[returnRulePriority][w.mark] {
				if err := netlink.RuleAdd(markRule(fam, returnRulePriority, int(w.mark), w.mark)); err != nil {
					errs = append(errs, fmt.Errorf("return path rule: %w", err))
				}
			}
		}
	}
	return errs
}

func markRule(fam, prio, table int, mark uint32) *netlink.Rule {
	r := netlink.NewRule()
	r.Family, r.Priority, r.Table, r.Mark = fam, prio, table, mark
	mask := uint32(0xffffffff)
	r.Mask = &mask
	return r
}

func flushTable(fam, table int) {
	rs, err := netlink.RouteListFiltered(fam, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return
	}
	for _, r := range rs {
		netlink.RouteDel(&r)
	}
}
