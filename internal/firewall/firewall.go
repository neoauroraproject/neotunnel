// Package firewall opens what NeoTunnel needs when the host runs ufw. Without
// an active ufw it does nothing (nftables forwarding needs no extra rules).
package firewall

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Needs struct {
	Ifaces   []string // tunnel interfaces: traffic in and routed through them
	UDPPorts []int    // tunnel ports (FOU, VXLAN, GENEVE)
	Ports    []string // ufw port specs, e.g. "7710/tcp", "7711"
	ESP      bool     // encrypted tunnels: IPsec ESP packets
	// Peers are trusted addresses: our own servers, and the far ends of
	// tunnels that use a raw IP protocol (GRE, IPIP, SIT, L2TPv3), which ufw
	// has no rule for. Everything from them is allowed.
	Peers []string
}

func (n Needs) rules() [][]string {
	var out [][]string
	for _, i := range n.Ifaces {
		out = append(out,
			[]string{"allow", "in", "on", i},
			[]string{"route", "allow", "in", "on", i},
			[]string{"route", "allow", "out", "on", i})
	}
	for _, p := range n.UDPPorts {
		out = append(out, []string{"allow", strconv.Itoa(p) + "/udp"})
	}
	for _, p := range n.Ports {
		out = append(out, []string{"allow", p})
	}
	for _, p := range n.Peers {
		out = append(out, []string{"allow", "from", p})
	}
	if n.ESP {
		out = append(out, []string{"allow", "proto", "esp", "from", "any", "to", "any"})
	}
	return out
}

var (
	mu   sync.Mutex
	done = map[string]bool{} // rules already added by this process
)

// Active reports whether ufw is installed and enabled.
func Active() bool {
	if _, err := exec.LookPath("ufw"); err != nil {
		return false
	}
	out, err := exec.Command("ufw", "status").Output()
	return err == nil && strings.Contains(string(out), "Status: active")
}

// Ensure adds any missing ufw rules. Rules are added once per process; ufw
// itself skips rules that already exist.
func Ensure(n Needs) error {
	mu.Lock()
	defer mu.Unlock()
	var todo [][]string
	for _, r := range n.rules() {
		if !done[strings.Join(r, " ")] {
			todo = append(todo, r)
		}
	}
	if len(todo) == 0 || !Active() {
		return nil
	}
	var failed []string
	for _, r := range todo {
		key := strings.Join(r, " ")
		if out, err := exec.Command("ufw", r...).CombinedOutput(); err != nil {
			failed = append(failed, fmt.Sprintf("ufw %s: %s", key, strings.TrimSpace(string(out))))
			continue
		}
		done[key] = true
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%s", strings.Join(failed, "; "))
	}
	return nil
}
