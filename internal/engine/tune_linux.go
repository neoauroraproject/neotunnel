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
// the others keep the kernel defaults.
func tune(s LinkSpec) error {
	l, err := netlink.LinkByName(s.Name)
	if err != nil {
		return err
	}
	qlen, rps := 1000, "0"
	if s.Profile == config.ProfileMax {
		qlen, rps = 10000, cpuMask(runtime.NumCPU())
	}
	if l.Attrs().TxQLen != qlen {
		if err := netlink.LinkSetTxQLen(l, qlen); err != nil {
			return err
		}
	}
	return setRPS(s.Name, rps, false)
}

// tuneNIC spreads the public interface's receive work over every core when
// it has fewer receive queues than the host has cores (common on virtual
// servers, where one core otherwise handles all tunnel traffic). It only
// turns this on; an admin's own setting is never undone.
func tuneNIC() {
	nic := PublicIface()
	if nic == "" {
		return
	}
	qs, _ := filepath.Glob(filepath.Join("/sys/class/net", nic, "queues", "rx-*"))
	if len(qs) == 0 || len(qs) >= runtime.NumCPU() {
		return
	}
	setRPS(nic, cpuMask(runtime.NumCPU()), true)
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
