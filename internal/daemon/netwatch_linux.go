package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// watchNet reconciles as soon as the server gets an address or a network
// card comes up (not one of ours). After a reboot the public address can
// arrive a little after the service starts; the tunnels then come up at
// once instead of at the next tick.
func watchNet(ctx context.Context, poke func()) {
	addrs := make(chan netlink.AddrUpdate, 64)
	links := make(chan netlink.LinkUpdate, 64)
	done := make(chan struct{})
	defer close(done)
	if err := netlink.AddrSubscribe(addrs, done); err != nil {
		return
	}
	if err := netlink.LinkSubscribe(links, done); err != nil {
		return
	}
	ours := func(index int) bool {
		l, err := netlink.LinkByIndex(index)
		return err == nil && strings.HasPrefix(l.Attrs().Name, "nt-")
	}
	var last time.Time
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-addrs:
			if !ok {
				return
			}
			if u.NewAddr && !ours(u.LinkIndex) {
				pending = time.After(500 * time.Millisecond)
			}
		case u, ok := <-links:
			if !ok {
				return
			}
			a := u.Attrs()
			if u.Header.Type == unix.RTM_NEWLINK && a.OperState == netlink.OperUp && !strings.HasPrefix(a.Name, "nt-") {
				pending = time.After(500 * time.Millisecond)
			}
		case <-pending:
			pending = nil
			if time.Since(last) > 2*time.Second {
				last = time.Now()
				poke()
			}
		}
	}
}
