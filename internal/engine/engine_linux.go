package engine

import (
	"fmt"
	"net/netip"
	"os/exec"
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
// no longer defined (and expired bench tunnels), installs IPsec, forward
// rules and return paths, and hands user-space parts to the daemon.
func Apply(st *config.State) Result {
	mu.Lock()
	defer mu.Unlock()

	var res Result
	if err := sysctl.EnsureForwarding(); err != nil {
		res.Errors = append(res.Errors, err)
	}
	specs, errs := Render(st, RouteSource)
	res.Specs, res.Errors = specs, append(res.Errors, errs...)
	loadModules(specs)

	res.Errors = append(res.Errors, syncFOU(specs)...)

	us := currentUserspace()
	if us != nil {
		var user []LinkSpec
		for _, s := range specs {
			if s.Userspace {
				user = append(user, s)
			}
		}
		res.Errors = append(res.Errors, us.SyncLinks(user)...)
	}

	want := map[string]bool{}
	for _, s := range specs {
		want[s.Name] = true
		if s.Userspace {
			if us == nil {
				continue // the daemon owns it
			}
			if err := adopt(s); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: %w", s.Tunnel, err))
			}
			if err := shape(s.Name, s.RateMbit); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: speed limit: %w", s.Tunnel, err))
			}
			if err := tune(s); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: profile: %w", s.Tunnel, err))
			}
			continue
		}
		created, err := ensure(s, aliasPersistent+s.Hash())
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: %w", s.Tunnel, err))
			continue
		}
		if created {
			res.Created = append(res.Created, s.Name)
		}
		if err := shape(s.Name, s.RateMbit); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: speed limit: %w", s.Tunnel, err))
		}
		if err := tune(s); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("tunnel %s: profile: %w", s.Tunnel, err))
		}
	}
	for _, s := range specs {
		if s.Profile == config.ProfileMax {
			tuneNIC()
			break
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
			if err := delLink(l); err == nil {
				res.Removed = append(res.Removed, name)
			}
		}
	}

	res.Errors = append(res.Errors, syncIPsec(specs)...)

	all, fs := st.Effective()
	fs = activeForwards(all, fs)
	links := make([]forward.Link, len(specs))
	for i, s := range specs {
		links[i] = forward.Link{Name: s.Name, Tunnel: s.Tunnel}
	}
	rules, ferrs := forward.Build(st, fs, links)
	rules.Protect = protects(specs)
	res.Errors = append(res.Errors, ferrs...)
	if err := forward.Apply(rules); err != nil {
		res.Errors = append(res.Errors, err)
	}
	res.Errors = append(res.Errors, syncReturns(rules.Returns, specs)...)
	if us != nil {
		res.Errors = append(res.Errors, us.SyncRelays(rules.Relays)...)
	}
	if err := firewall.Ensure(firewallNeeds(st, specs, rules)); err != nil {
		res.Errors = append(res.Errors, err)
	}
	return res
}

// adopt configures a TUN/TAP interface the daemon's data plane created.
func adopt(s LinkSpec) error {
	l, err := netlink.LinkByName(s.Name)
	if err != nil {
		return err
	}
	if alias := aliasUser + s.Hash(); l.Attrs().Alias != alias {
		if err := netlink.LinkSetAlias(l, alias); err != nil {
			return err
		}
	}
	return configure(l, s)
}

// ServicePorts are the fixed ports every node listens on.
var ServicePorts = []string{"7710/tcp", "7711"}

// firewallNeeds lists what a host firewall (ufw) must let through.
func firewallNeeds(st *config.State, specs []LinkSpec, rs forward.Ruleset) firewall.Needs {
	n := firewall.Needs{Ports: append([]string(nil), ServicePorts...)}
	if st.Panel != nil {
		n.Ports = append(n.Ports, fmt.Sprintf("%d/tcp", st.Panel.Port))
	}
	for _, s := range specs {
		n.Ifaces = append(n.Ifaces, s.Name)
		if s.Port != 0 && s.Kind == config.KindTCP {
			n.Ports = append(n.Ports, fmt.Sprintf("%d/tcp", s.Port))
		} else if s.Port != 0 {
			n.UDPPorts = append(n.UDPPorts, int(s.Port))
		}
		if s.Encrypt != "" {
			n.ESP = true
		}
		if s.Parent == "" && !s.Userspace && s.Remote.IsValid() {
			if p, port := outerProto(s); port == 0 && p != 17 {
				n.Peers = append(n.Peers, s.Remote.String())
			}
		}
	}
	for _, r := range rs.Relays {
		n.Ports = append(n.Ports, fmt.Sprintf("%d/%s", r.Port, r.Proto))
	}
	// Our own servers are trusted for everything: the temporary tunnels of
	// speed tests and wizard checks use ports and protocols no saved tunnel
	// has opened yet.
	for _, p := range st.Peers {
		if a := hostOnly(p.Address); a != "" {
			n.Peers = append(n.Peers, a)
		}
	}
	if m := st.Managed; m != nil {
		if a := hostOnly(m.Master.Address); a != "" {
			n.Peers = append(n.Peers, a)
		}
	}
	return n
}

// hostOnly is the IP of an "ip" or "ip:port" address, or "".
func hostOnly(s string) string {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().String()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return ""
}

var (
	modMu  sync.Mutex
	modsOK = map[string]bool{}
)

// loadModules loads the kernel modules the specs need, once per process,
// for systems where they aren't loaded at boot.
func loadModules(specs []LinkSpec) {
	need := map[string]bool{}
	for _, s := range specs {
		for _, m := range modulesFor(s) {
			need[m] = true
		}
	}
	modMu.Lock()
	defer modMu.Unlock()
	for m := range need {
		if !modsOK[m] {
			modsOK[m] = true
			exec.Command("modprobe", "-q", m).Run()
		}
	}
}

func modulesFor(s LinkSpec) []string {
	var ms []string
	switch s.Kind {
	case config.KindSIT:
		ms = []string{"sit"}
	case config.KindIPIP:
		ms = []string{"ipip"}
	case config.KindGRE, config.KindGRETAP:
		ms = []string{"ip_gre"}
	case config.KindIP6GRE, config.KindIP6GRETAP:
		ms = []string{"ip6_gre"}
	case config.KindIPIP6, config.KindIP6IP6:
		ms = []string{"ip6_tunnel"}
	case config.KindVXLAN:
		ms = []string{"vxlan"}
	case config.KindGENEVE:
		ms = []string{"geneve"}
	case config.KindL2TPv3:
		ms = []string{"l2tp_eth", "l2tp_netlink", "l2tp_ip", "l2tp_ip6"}
	case config.KindWireGuard:
		ms = []string{"wireguard"}
	case config.KindObfs, config.KindTCP, config.KindEoIP:
		ms = []string{"tun"}
	}
	if s.FOU {
		ms = append(ms, "fou")
	}
	if s.Encrypt != "" {
		ms = append(ms, "xfrm_user", "esp4", "esp6")
	}
	return ms
}

// ApplyBench creates temporary tunnels that the daemon removes after ttl even
// if the test run dies midway.
func ApplyBench(specs []LinkSpec, ttl time.Duration) error {
	mu.Lock()
	defer mu.Unlock()
	loadModules(specs)
	expiry := time.Now().Add(ttl)
	if err := addBenchFOU(specs, expiry); err != nil {
		return err
	}
	exp := expiry.Unix()
	for _, s := range specs {
		if s.Userspace {
			if err := benchUserspace(s, expiry); err != nil {
				return fmt.Errorf("%s: %w", s.Name, err)
			}
			continue
		}
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
	if us := currentUserspace(); us != nil {
		us.StopBench(names)
	}
	for i := len(names) - 1; i >= 0; i-- {
		if l, err := netlink.LinkByName(names[i]); err == nil && strings.HasPrefix(l.Attrs().Alias, aliasBench) {
			delLink(l)
		}
	}
}

// Down removes every interface NeoTunnel owns, its IPsec, return paths and
// forward rules. User-space tunnels go away with the daemon.
func Down() error {
	mu.Lock()
	defer mu.Unlock()
	if us := currentUserspace(); us != nil {
		us.SyncLinks(nil)
		us.SyncRelays(nil)
	}
	have, err := owned()
	if err != nil {
		return err
	}
	for _, l := range have {
		delLink(l)
	}
	removeAllFOU()
	removeIPsec()
	syncReturns(nil, nil)
	return forward.Apply(forward.Ruleset{})
}

// benchUserspace has the daemon's data plane run a temporary user-space
// tunnel, then configures its interface like adopt does.
func benchUserspace(s LinkSpec, expiry time.Time) error {
	us := currentUserspace()
	if us == nil {
		return ErrNeedsDaemon
	}
	if err := us.StartBench(s, expiry); err != nil {
		return err
	}
	l, err := netlink.LinkByName(s.Name)
	if err != nil {
		return err
	}
	if err := netlink.LinkSetAlias(l, fmt.Sprintf("%s%d:%s", aliasBench, expiry.Unix(), s.Hash())); err != nil {
		return err
	}
	return configure(l, s)
}
