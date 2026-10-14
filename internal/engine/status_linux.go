package engine

import (
	"net"

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
	}
	return s
}
