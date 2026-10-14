package forward

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// The daemon calls Apply every reconcile tick; an unchanged ruleset is only
// reloaded every refreshEvery, to repair external flushes.
const refreshEvery = 5 * time.Minute

var (
	mu       sync.Mutex
	lastSet  string
	lastLoad time.Time
)

// Apply loads the ruleset into nftables in a single transaction.
func Apply(rs Ruleset) error {
	script := Script(rs)
	mu.Lock()
	defer mu.Unlock()
	if script == lastSet && time.Since(lastLoad) < refreshEvery {
		return nil
	}
	if _, err := exec.LookPath("nft"); err != nil {
		if len(rs.Rules) == 0 {
			return nil // nothing to install, nothing to clean up
		}
		return fmt.Errorf("nftables is required for port forwarding: apt install nftables")
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		lastSet = ""
		return fmt.Errorf("nft: %v: %s", err, strings.TrimSpace(out.String()))
	}
	lastSet, lastLoad = script, time.Now()
	return nil
}
