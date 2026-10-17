package engine

import (
	"github.com/vishvananda/netlink"
)

// shapeHandle marks the qdisc as ours, so a limit the admin set by hand on
// a tunnel is never removed.
var shapeHandle = netlink.MakeHandle(0x7a7a, 0)

// shape caps what an interface sends with a token bucket (mbit = 0 removes
// the cap). Both ends shape their own side, which limits both directions.
func shape(name string, mbit int) error {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	qs, err := netlink.QdiscList(l)
	if err != nil {
		return err
	}
	var cur *netlink.Tbf
	for _, q := range qs {
		if t, ok := q.(*netlink.Tbf); ok && q.Attrs().Parent == netlink.HANDLE_ROOT && q.Attrs().Handle == shapeHandle {
			cur = t
		}
	}
	if mbit <= 0 {
		if cur != nil {
			return netlink.QdiscDel(cur)
		}
		return nil
	}
	rate := uint64(mbit) * 125_000 // bytes per second
	if cur != nil && cur.Rate == rate {
		return nil
	}
	// A burst of 10 ms at the rate (at least 32 KiB, so fast links still
	// fill), and up to 50 ms of queue before packets drop.
	burst := uint32(max(rate/100, 32<<10))
	return netlink.QdiscReplace(&netlink.Tbf{
		QdiscAttrs: netlink.QdiscAttrs{LinkIndex: l.Attrs().Index, Handle: shapeHandle, Parent: netlink.HANDLE_ROOT},
		Rate:       rate,
		Buffer:     netlink.Xmittime(rate, burst),
		Limit:      uint32(rate/20) + burst,
	})
}
