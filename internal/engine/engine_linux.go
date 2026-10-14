package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/config"
	"neotunnel/internal/firewall"
	"neotunnel/internal/forward"
	"neotunnel/internal/sysctl"
)

// mu serializes kernel changes within a process (daemon loop vs API calls).
var mu sync.Mutex

// Apply converges the kernel to st: creates/updates our tunnels, removes ones
// no longer defined (and expired bench tunnels), and installs forward rules.
func Apply(st *config.State) Result {
	mu.Lock()
	defer mu.Unlock()

	var res Result
	if err := sysctl.EnsureForwarding(); err != nil {
		res.Errors = append(res.Errors, err)
	}
	specs, errs := Render(st, RouteSource)
	res.Specs, res.Errors = specs, append(res.Errors, errs...)

	res.Errors = append(res.Errors, syncFOU(specs)...)

	want := map[string]bool{}
	for _, s := range specs {
		want[s.Name] = true
		created, err := ensure(s, aliasPersistent+s.Hash())
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: %w", s.Tunnel, err))
			continue
		}
		if created {
			res.Created = append(res.Created, s.Name)
		}
	}

	have, err := owned()
	if err != nil {
		res.Errors = append(res.Errors, err)
		return res
	}
	now := time.Now()
	for name, l := range have {
		alias := l.Attrs().Alias
		stale := strings.HasPrefix(alias, aliasPersistent) && !want[name]
		if stale || benchExpired(alias, now) {
			if err := netlink.LinkDel(l); err == nil {
				res.Removed = append(res.Removed, name)
			}
		}
	}

	_, fs := st.Effective()
	links := make([]forward.Link, len(specs))
	for i, s := range specs {
		links[i] = forward.Link{Name: s.Name, Tunnel: s.Tunnel}
	}
	rules, ferrs := forward.Build(st, fs, links)
	res.Errors = append(res.Errors, ferrs...)
	if err := forward.Apply(rules); err != nil {
		res.Errors = append(res.Errors, err)
	}
	if err := firewall.Ensure(firewallNeeds(st, specs)); err != nil {
		res.Errors = append(res.Errors, err)
	}
	return res
}

// ServicePorts are the fixed ports every node listens on.
var ServicePorts = []string{"7710/tcp", "7711"}

// firewallNeeds lists what a host firewall (ufw) must let through.
func firewallNeeds(st *config.State, specs []LinkSpec) firewall.Needs {
	n := firewall.Needs{Ports: append([]string(nil), ServicePorts...)}
	if st.Panel != nil {
		n.Ports = append(n.Ports, fmt.Sprintf("%d/tcp", st.Panel.Port))
	}
	for _, s := range specs {
		n.Ifaces = append(n.Ifaces, s.Name)
		if s.Port != 0 {
			n.UDPPorts = append(n.UDPPorts, int(s.Port))
		}
	}
	return n
}

// ApplyBench creates temporary tunnels that the daemon removes after ttl even
// if the test run dies midway.
func ApplyBench(specs []LinkSpec, ttl time.Duration) error {
	mu.Lock()
	defer mu.Unlock()
	expiry := time.Now().Add(ttl)
	if err := addBenchFOU(specs, expiry); err != nil {
		return err
	}
	exp := expiry.Unix()
	for _, s := range specs {
		if _, err := ensure(s, fmt.Sprintf("%s%d:%s", aliasBench, exp, s.Hash())); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return nil
}

// RemoveBench deletes temporary tunnels by name (children first).
func RemoveBench(names []string) {
	mu.Lock()
	defer mu.Unlock()
	for i := len(names) - 1; i >= 0; i-- {
		if l, err := netlink.LinkByName(names[i]); err == nil && strings.HasPrefix(l.Attrs().Alias, aliasBench) {
			netlink.LinkDel(l)
		}
	}
}

// Down removes every interface NeoTunnel owns and its forward rules.
func Down() error {
	mu.Lock()
	defer mu.Unlock()
	have, err := owned()
	if err != nil {
		return err
	}
	for _, l := range have {
		netlink.LinkDel(l)
	}
	removeAllFOU()
	return forward.Apply(forward.Ruleset{})
}
