package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/config"
	"neotunnel/internal/failover"
	"neotunnel/internal/forward"
)

// Doctor inspects the node and, when fix is true, removes leftovers that no
// running tunnel needs: stale or expired interfaces, orphan FOU ports, IPsec
// SAs and policies, and transparent-forward routing rules. It never touches
// interfaces a current tunnel still uses.
func Doctor(st *config.State, fix bool) []Finding {
	mu.Lock()
	defer mu.Unlock()
	var out []Finding
	add := func(ok, fixed bool, f string, a ...any) {
		out = append(out, Finding{OK: ok, Fixed: fixed, Message: fmt.Sprintf(f, a...)})
	}

	specs, errs := Render(st, RouteSource)
	for _, e := range errs {
		add(false, false, "tunnel definition error: %v", e)
	}
	want := map[string]bool{}
	for _, s := range specs {
		want[s.Name] = true
	}

	// Interfaces we own that no current tunnel wants, or expired bench ones.
	have, err := owned()
	if err != nil {
		add(false, false, "could not list interfaces: %v", err)
	}
	now := time.Now()
	leftover := 0
	for name, l := range have {
		alias := l.Attrs().Alias
		stale := strings.HasPrefix(alias, aliasPersistent) && !want[name]
		userStale := strings.HasPrefix(alias, aliasUser) && !want[name]
		if stale || userStale || benchExpired(alias, now) {
			leftover++
			if fix {
				if delLink(l) == nil {
					add(false, true, "removed leftover interface %s", name)
				}
			} else {
				add(false, false, "leftover interface %s (no tunnel uses it)", name)
			}
		}
	}
	if leftover == 0 {
		add(true, false, "no leftover interfaces")
	}

	// IPsec and return-path rules for tunnels that are gone: syncing to the
	// current specs removes ours that aren't wanted.
	if fix {
		if e := syncIPsec(specs); len(e) > 0 {
			for _, err := range e {
				add(false, false, "IPsec: %v", err)
			}
		} else {
			add(true, true, "IPsec state matches the current tunnels")
		}
		links := make([]forward.Link, len(specs))
		for i, s := range specs {
			links[i] = forward.Link{Name: s.Name, Tunnel: s.Tunnel}
		}
		all, fs := st.Effective()
		rules, _ := forward.Build(st, failover.Route(fs, all, failover.Load()), links)
		syncReturns(rules.Returns, specs)
		add(true, true, "routing rules match the current forwards")
	} else {
		pols, _ := netlink.XfrmPolicyList(netlink.FAMILY_ALL)
		stale := 0
		for _, p := range pols {
			if p.Priority == policyPriority {
				stale++
			}
		}
		if stale > len(specs) {
			add(false, false, "%d IPsec policies, more than the %d tunnels: run with --fix", stale, len(specs))
		}
	}

	// How the card's receive work is spread over the cores.
	out = append(out, SpreadReport(st.Address)...)
	return out
}

// DoctorFOU checks the FOU receive ports (outside the kernel-link lock).
func DoctorFOU(st *config.State, fix bool) []Finding {
	specs, _ := Render(st, RouteSource)
	want := map[string]bool{}
	for _, k := range fouKeys(specs) {
		want[k.String()] = true
	}
	var out []Finding
	n := 0
	config.WithLock(func() error {
		owned := loadOwned()
		for key := range owned {
			if !want[key] {
				n++
				if fix {
					fouDel(key)
					delete(owned, key)
				}
			}
		}
		if fix {
			saveOwned(owned)
		}
		return nil
	})
	switch {
	case n == 0:
		out = append(out, Finding{OK: true, Message: "no orphan UDP (FOU) ports"})
	case fix:
		out = append(out, Finding{Fixed: true, Message: fmt.Sprintf("closed %d orphan UDP (FOU) port(s)", n)})
	default:
		out = append(out, Finding{Message: fmt.Sprintf("%d orphan UDP (FOU) port(s): run with --fix", n)})
	}
	return out
}
