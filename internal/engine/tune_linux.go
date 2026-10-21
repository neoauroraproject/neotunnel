package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/config"
)

// tune applies a spec's speed profile to its interface.
//
// A tunnel interface has a single receive queue, so the kernel handles all
// of its incoming packets on one CPU core. The max profile spreads them over
// every core (RPS) and gives the interface a long transmit queue for bursts;
// the others keep the kernel defaults. When the public card already spreads
// its packets by their inner flow (tuneNIC), a second spread here would only
// move most packets to yet another core, so the tunnel leaves it off.
func tune(s LinkSpec) error {
	l, err := netlink.LinkByName(s.Name)
	if err != nil {
		return err
	}
	qlen, rps := 1000, "0"
	if s.Profile == config.ProfileMax {
		qlen = 10000
		if !nicSpreads(s.Local.String()) {
			rps = cpuMask(runtime.NumCPU())
		}
	}
	if l.Attrs().TxQLen != qlen {
		if err := netlink.LinkSetTxQLen(l, qlen); err != nil {
			return err
		}
	}
	return setRPS(s.Name, rps, false)
}

// setRPS writes the CPU mask of every receive queue of dev. With onlyIfOff,
// queues that already have a mask are left alone.
func setRPS(dev, mask string, onlyIfOff bool) error {
	qs, err := filepath.Glob(filepath.Join("/sys/class/net", dev, "queues", "rx-*", "rps_cpus"))
	if err != nil {
		return err
	}
	for _, q := range qs {
		cur, err := os.ReadFile(q)
		if err != nil {
			continue
		}
		if onlyIfOff && strings.Trim(strings.TrimSpace(string(cur)), "0,") != "" {
			continue
		}
		if sameMask(string(cur), mask) {
			continue
		}
		if err := os.WriteFile(q, []byte(mask), 0o644); err != nil {
			return fmt.Errorf("rps on %s: %w", dev, err)
		}
	}
	return nil
}
