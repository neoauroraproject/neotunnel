package engine

// Spreading the host's receive work over its cores (the max profile).
//
// A network card hashes each packet's addresses (and, for TCP, ports) to
// pick a receive queue, and each queue's interrupt runs on one core. A
// kernel tunnel between two servers is a single outer flow: raw GRE, IPIP
// and SIT have no ports, so every packet of the tunnel lands in the same
// queue and on the same core, however many connections run inside it and
// however many lanes it has (lanes differ only in the GRE key, which cards
// don't hash). That core then does all of the receive work: the driver,
// GRO, the outer IP and GRE layers, and the inner packets.
//
// Three things move work off it:
//
//   - RPS on the card's queues: after the driver hands a packet over, the
//     kernel's flow dissector looks inside GRE, IPIP and SIT, hashes the
//     inner flow, and continues on another core. Each queue's mask leaves
//     out the core its own interrupt runs on, so that core keeps only the
//     driver and GRO and everything after them is spread.
//   - UDP hashing on the card (rx-flow-hash udp4/udp6 with ports): tunnels
//     carried in UDP (FOU ports, VXLAN, GENEVE, WireGuard, NeoObfs) get a
//     source port per inner flow, so the card itself puts them in
//     different queues, the way separate TCP connections are spread.
//   - A larger softirq budget, so a busy core finishes a burst instead of
//     deferring the rest to the next round.
//
// Masks NeoTunnel wrote are remembered in /run (which, like the masks
// themselves, does not survive a reboot), so they follow an interrupt that
// moved; a mask an admin set is never changed.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/sysctl"
)

const rpsStateFile = "/run/neotunnel/rps.json"

// softirq budget for the max profile: packets per round, and its time.
var spreadSysctls = [][2]string{
	{"net.core.netdev_budget", "600"},
	{"net.core.netdev_budget_usecs", "8000"},
}

var (
	spreadMu sync.Mutex
	// udpHashDone: NICs whose UDP hashing was checked by this process.
	udpHashDone = map[string]string{}
)

// NICQueue is one receive queue of a card and where its interrupt runs.
type NICQueue struct {
	Index int
	IRQ   int   // 0 = unknown
	CPUs  []int // cores the interrupt runs on; empty = unknown
	RPS   string
}

// nicQueues lists nic's receive queues with their interrupts' cores, as far
// as the interrupt names tell (ens192-rxtx-3, eth0-TxRx-1, virtio0-input.2,
// ena-rx...: the queue is the number at the end of the name).
func nicQueues(nic string) []NICQueue {
	paths, _ := filepath.Glob(filepath.Join("/sys/class/net", nic, "queues", "rx-*"))
	var out []NICQueue
	for _, p := range paths {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "rx-"))
		if err != nil {
			continue
		}
		mask, _ := os.ReadFile(filepath.Join(p, "rps_cpus"))
		out = append(out, NICQueue{Index: n, RPS: strings.TrimSpace(string(mask))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	irqs := queueIRQs(nic)
	for i := range out {
		if irq, ok := irqs[out[i].Index]; ok {
			out[i].IRQ = irq
			out[i].CPUs = irqCPUs(irq)
		}
	}
	return out
}

// queueIRQs maps receive queue numbers to interrupt numbers from
// /proc/interrupts. Lines that only transmit, or carry events or control
// messages, are skipped.
func queueIRQs(nic string) map[int]int {
	names := []string{nic}
	if dev, err := os.Readlink(filepath.Join("/sys/class/net", nic, "device")); err == nil {
		names = append(names, filepath.Base(dev)) // virtio0
	}
	f, err := os.Open("/proc/interrupts")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[int]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		irq, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
		if err != nil {
			continue
		}
		name := fields[len(fields)-1]
		if !rxIRQName(name, names) {
			continue
		}
		m := trailingNum.FindString(name)
		if m == "" {
			continue
		}
		q, _ := strconv.Atoi(m)
		if _, dup := out[q]; !dup {
			out[q] = irq
		}
	}
	return out
}

// irqCPUs reads the cores an interrupt runs on.
func irqCPUs(irq int) []int {
	for _, f := range []string{"effective_affinity_list", "smp_affinity_list"} {
		b, err := os.ReadFile(fmt.Sprintf("/proc/irq/%d/%s", irq, f))
		if err != nil {
			continue
		}
		if cpus := parseCPUList(strings.TrimSpace(string(b))); len(cpus) > 0 {
			return cpus
		}
	}
	return nil
}

// tuneNIC spreads the public interface's receive work (see the top of the
// file). It runs on every reconcile while a tunnel uses the max profile,
// and only does work when something changed.
func tuneNIC(address string) {
	nic := spreadNIC(address)
	if nic == "" {
		return
	}
	spreadMu.Lock()
	defer spreadMu.Unlock()

	ours := loadRPSState()
	changed := false
	ncpu := runtime.NumCPU()
	for _, q := range nicQueues(nic) {
		path := filepath.Join("/sys/class/net", nic, "queues", fmt.Sprintf("rx-%d", q.Index), "rps_cpus")
		want := spreadMask(ncpu, q.CPUs)
		cur := q.RPS
		// An admin's own mask (not zero, not one we wrote) stays.
		if !maskEmpty(cur) && !sameMask(cur, ours[path]) {
			continue
		}
		if sameMask(cur, want) {
			continue
		}
		if os.WriteFile(path, []byte(want), 0o644) == nil {
			ours[path] = want
			changed = true
		}
	}
	if changed {
		saveRPSState(ours)
	}
	for _, kv := range spreadSysctls {
		raiseSysctl(kv[0], kv[1])
	}
	if _, done := udpHashDone[nic]; !done {
		udpHashDone[nic] = enableUDPHash(nic)
	}
}

// nicSpreads reports whether every receive queue of the public card passes
// its work on (RPS), so the inner flows are already spread when they reach
// a tunnel.
func nicSpreads(address string) bool {
	nic := spreadNIC(address)
	if nic == "" {
		return false
	}
	qs, _ := filepath.Glob(filepath.Join("/sys/class/net", nic, "queues", "rx-*", "rps_cpus"))
	for _, q := range qs {
		if b, err := os.ReadFile(q); err != nil || maskEmpty(string(b)) {
			return false
		}
	}
	return len(qs) > 0
}

// spreadNIC is the card the tunnels' outer packets arrive on: the one that
// holds this node's public address, else the one with the default route.
func spreadNIC(address string) string {
	if a, err := netip.ParseAddr(address); err == nil {
		if links, err := netlink.LinkList(); err == nil {
			for _, l := range links {
				addrs, _ := netlink.AddrList(l, netlink.FAMILY_ALL)
				for _, x := range addrs {
					if ip, ok := netip.AddrFromSlice(x.IP); ok && ip.Unmap() == a.Unmap() {
						return l.Attrs().Name
					}
				}
			}
		}
	}
	return PublicIface()
}

func maskEmpty(m string) bool { return strings.Trim(strings.TrimSpace(m), "0,") == "" }

// raiseSysctl sets key to value when it is lower now.
func raiseSysctl(key, value string) {
	cur, err := sysctl.Get(key)
	if err != nil {
		return
	}
	c, err1 := strconv.Atoi(strings.TrimSpace(cur))
	v, err2 := strconv.Atoi(value)
	if err1 == nil && err2 == nil && c < v {
		sysctl.Set(key, value)
	}
}

// enableUDPHash makes the card hash UDP ports too (ethtool rx-flow-hash
// sdfn), so UDP tunnels with per-flow source ports spread over its queues.
// It returns what it found: "on", "turned on", "unsupported" or "no ethtool".
func enableUDPHash(nic string) string {
	if _, err := exec.LookPath("ethtool"); err != nil {
		return "no ethtool"
	}
	state := "on"
	for _, fam := range []string{"udp4", "udp6"} {
		if udpHashOn(nic, fam) {
			continue
		}
		if err := exec.Command("ethtool", "-N", nic, "rx-flow-hash", fam, "sdfn").Run(); err != nil || !udpHashOn(nic, fam) {
			if fam == "udp4" {
				return "unsupported"
			}
			continue
		}
		state = "turned on"
	}
	return state
}

func udpHashOn(nic, fam string) bool {
	out, err := exec.Command("ethtool", "-n", nic, "rx-flow-hash", fam).Output()
	return err == nil && strings.Contains(string(out), "L4 bytes 0 & 1")
}

func loadRPSState() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(rpsStateFile); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func saveRPSState(m map[string]string) {
	if b, err := json.Marshal(m); err == nil {
		os.MkdirAll(filepath.Dir(rpsStateFile), 0o755)
		os.WriteFile(rpsStateFile, b, 0o644)
	}
}

// SpreadReport describes how the card's receive work is spread, for the
// doctor. Only a gap that matters is an issue: with a tunnel on the max
// profile, a queue that does not hand its work on, or UDP hashing that is
// off although the card could do it. The rest is information.
func SpreadReport(address string, maxProfile bool) []Finding {
	nic := spreadNIC(address)
	if nic == "" {
		return nil
	}
	var out []Finding
	add := func(ok bool, f string, a ...any) { out = append(out, Finding{OK: ok, Message: fmt.Sprintf(f, a...)}) }
	qs := nicQueues(nic)
	ncpu := runtime.NumCPU()
	var where []string
	spread := 0
	for _, q := range qs {
		c := "unknown core"
		if len(q.CPUs) > 0 {
			c = "core " + strings.Trim(strings.Join(strings.Fields(fmt.Sprint(q.CPUs)), ","), "[]")
		}
		where = append(where, fmt.Sprintf("rx-%d on %s", q.Index, c))
		if !maskEmpty(q.RPS) {
			spread++
		}
	}
	add(true, "%s: %d receive queue(s), %d core(s): %s", nic, len(qs), ncpu, strings.Join(where, ", "))
	if ncpu > 1 {
		switch {
		case !maxProfile:
			add(true, "RPS on %s: %d of %d queue(s) hand their work to other cores (the max speed profile turns this on)", nic, spread, len(qs))
		default:
			add(spread == len(qs), "RPS on %s: %d of %d queue(s) hand their work to other cores", nic, spread, len(qs))
		}
	}
	spreadMu.Lock()
	udp, known := udpHashDone[nic]
	spreadMu.Unlock()
	if !known {
		if _, err := exec.LookPath("ethtool"); err != nil {
			udp = "no ethtool"
		} else if udpHashOn(nic, "udp4") {
			udp = "on"
		} else {
			udp = "off"
		}
	}
	add(udp != "off" || !maxProfile, "UDP port hashing on %s: %s (tunnels on a UDP port spread over the card's queues with it)", nic, udp)
	return out
}
