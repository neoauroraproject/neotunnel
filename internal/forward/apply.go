package forward

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/sysctl"
)

// The daemon calls Apply every reconcile tick; an unchanged ruleset is only
// reloaded every refreshEvery, to repair external flushes.
const refreshEvery = 5 * time.Minute

var (
	mu       sync.Mutex
	lastSet  string
	lastLoad time.Time
	// lastFlows is where the loaded rules send each port (nil before the
	// first load of this process: at start nothing is forgotten).
	lastFlows map[string]string
	// degraded is why the loaded rules lack the counters and limits.
	degraded error
	// keep are the forwards (by key) whose open connections the next load
	// leaves on the path they took (see KeepFlows).
	keep map[string]bool
)

// KeepFlows makes the next load leave the open connections of a forward
// (by config.Forward.PortKey) on the tunnel they took: it moved to a better
// one while the old still works, so only new connections take the new
// path and nothing open is cut.
func KeepFlows(key string) {
	mu.Lock()
	defer mu.Unlock()
	if keep == nil {
		keep = map[string]bool{}
	}
	keep[key] = true
}

func nft(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nft: %v: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Apply loads the ruleset into nftables: forwarding in one transaction, the
// IPsec protection table in a second one.
func Apply(rs Ruleset) error {
	main, protect, fetch, raw, seen := Script(rs), ProtectScript(rs.Protect), FetchScript(rs.Fetch), CarrierScript(rs.Carriers), SeenScript(rs.Accounts)
	// What the plans' script is made of, without what the table has now.
	plans := PlansScript(rs.Plans, nil, nil)
	mu.Lock()
	defer mu.Unlock()
	// Every time, not only on changes: a tunnel that came back up has its
	// interface settings back at the defaults.
	lerr := setLocalnet(rs)
	all := main + protect + fetch + raw + seen + plans
	if all == lastSet && time.Since(lastLoad) < refreshEvery {
		return errors.Join(lerr, degraded)
	}
	if _, err := exec.LookPath("nft"); err != nil {
		if len(rs.Rules) == 0 && len(rs.Returns) == 0 && len(rs.Protect) == 0 && rs.Fetch == nil && len(rs.Carriers) == 0 {
			return nil // nothing to install, nothing to clean up
		}
		return fmt.Errorf("nftables is required for port forwarding and encryption: apt install nftables")
	}
	var extra []error
	// The table is replaced whole, its counters with it: they start again
	// where they were, so no port loses the bytes since the last sample.
	if err := nft(keepCounts(main, ReadCounters())); err != nil {
		if len(rs.Accounts) == 0 {
			lastSet = ""
			return err
		}
		// An nftables or kernel too old for the counters must not take
		// the forwards down with them.
		bare := rs
		bare.Accounts = nil
		if err2 := nft(Script(bare)); err2 != nil {
			lastSet = ""
			return err
		}
		extra = append(extra, fmt.Errorf("ports work, but their traffic isn't counted (nftables or kernel too old?): %w", err))
	}
	// The sold ports' limits, apart, so a failure leaves the ports working.
	chains, sets := planObjects()
	if err := nft(PlansScript(rs.Plans, chains, sets)); err != nil && len(rs.Plans) > 0 {
		extra = append(extra, fmt.Errorf("sold ports' limits aren't enforced (nftables or kernel too old?): %w", err))
	}
	if err := nft(protect); err != nil {
		lastSet = ""
		return fmt.Errorf("encrypted tunnels accept unencrypted packets too (nftables too old?): %w", err)
	}
	if err := nft(fetch); err != nil {
		lastSet = ""
		if rs.Fetch != nil && !rs.Fetch.All {
			return fmt.Errorf("downloads through the tunnel: this server can't tell terminal commands apart (nftables or kernel too old); choose all programs instead: %w", err)
		}
		return fmt.Errorf("downloads through the tunnel: %w", err)
	}
	// Only a cost saved: tunnels work without it (an old nftables), so its
	// failure is not reported.
	nft(raw)
	// Only the clients report needs it (see SeenScript).
	nft(seen)
	lastSet, lastLoad, degraded = all, time.Now(), errors.Join(extra...)
	flows := flowTargets(rs)
	if lastFlows != nil {
		changed := changedFlows(lastFlows, flows)
		for _, r := range rs.Rules {
			if keep[r.Key] {
				for _, p := range r.Proto {
					for _, port := range Expand(r.Ports) {
						delete(changed, fmt.Sprintf("%s/%d", p, port))
					}
				}
			}
		}
		forgetFlows(changed)
	}
	lastFlows, keep = flows, nil
	return errors.Join(lerr, degraded)
}

// planObjects lists the chains and sets the plans table has now.
func planObjects() (chains, sets []string) {
	out, err := exec.Command("nft", "-j", "list", "table", "inet", PlansTable).Output()
	if err != nil {
		return nil, nil // no table yet
	}
	var doc struct {
		Nftables []struct {
			Chain *struct{ Name string } `json:"chain"`
			Set   *struct{ Name string } `json:"set"`
		} `json:"nftables"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil, nil
	}
	for _, o := range doc.Nftables {
		switch {
		case o.Chain != nil:
			chains = append(chains, o.Chain.Name)
		case o.Set != nil:
			sets = append(sets, o.Set.Name)
		}
	}
	return chains, sets
}

// StopFetch removes the downloads redirect alone: without the daemon's
// listener behind it, this server's own HTTP and HTTPS would go nowhere.
func StopFetch() {
	mu.Lock()
	defer mu.Unlock()
	if _, err := exec.LookPath("nft"); err == nil {
		nft(FetchScript(nil))
	}
	lastSet = ""
}

// setLocalnet turns route_localnet on for the tunnels that need it (see
// localnetIfaces) and off on the others.
func setLocalnet(rs Ruleset) error {
	want := localnetIfaces(rs)
	var failed []string
	for _, i := range rs.Ifaces {
		v := "0"
		if want[i] {
			v = "1"
		}
		if err := sysctl.SetIface(i, "route_localnet", v); err != nil && want[i] {
			failed = append(failed, i)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("forwards can't be reached at 127.0.0.1 from this server through %s: route_localnet not set", strings.Join(failed, ", "))
	}
	return nil
}
