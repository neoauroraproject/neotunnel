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
	if main+protect == lastSet && time.Since(lastLoad) < refreshEvery {
		return nil
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
	return nil
}
