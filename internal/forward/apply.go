package forward

import (
	"bytes"
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
)

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
	main, protect := Script(rs), ProtectScript(rs.Protect)
	mu.Lock()
	defer mu.Unlock()
	// Every time, not only on changes: a tunnel that came back up has its
	// interface settings back at the defaults.
	lerr := setLocalnet(rs)
	if main+protect == lastSet && time.Since(lastLoad) < refreshEvery {
		return lerr
	}
	if _, err := exec.LookPath("nft"); err != nil {
		if len(rs.Rules) == 0 && len(rs.Returns) == 0 && len(rs.Protect) == 0 {
			return nil // nothing to install, nothing to clean up
		}
		return fmt.Errorf("nftables is required for port forwarding and encryption: apt install nftables")
	}
	if err := nft(main); err != nil {
		lastSet = ""
		return err
	}
	if err := nft(protect); err != nil {
		lastSet = ""
		return fmt.Errorf("encrypted tunnels accept unencrypted packets too (nftables too old?): %w", err)
	}
	lastSet, lastLoad = main+protect, time.Now()
	return lerr
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
