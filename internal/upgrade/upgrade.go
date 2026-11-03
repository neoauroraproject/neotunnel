// Package upgrade makes a server's upgrade by its main server undoable:
// the old binary is kept, and a watchdog started from it, outside the
// service, waits for the new version to answer the main server. If it
// doesn't within Deadline (it won't start, or it can't reach the main
// server any more), the watchdog puts the old binary back and restarts the
// service, and the server tells its main server at the next contact that
// the version was rolled back.
package upgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/version"
)

// Deadline is how long a new version has to answer its main server.
const Deadline = 4 * time.Minute

// Previous is where the binary before an upgrade is kept.
func Previous(exe string) string { return exe + ".previous" }

func okPath() string     { return filepath.Join(config.Dir, "upgrade-ok") }
func failedPath() string { return filepath.Join(config.Dir, "upgrade-failed") }

// Keep copies the running binary to Previous before it is replaced.
func Keep(exe string) error {
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := Previous(exe) + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, Previous(exe))
}

// Watch starts the watchdog for the new version want, from the kept old
// binary, in a unit of its own: the service's restart doesn't take it down.
// Without systemd-run there is no watchdog (the upgrade still happens).
func Watch(exe, want string) error {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return err
	}
	os.Remove(okPath())
	unit := "neotunnel-upgrade-watch-" + strconv.FormatInt(time.Now().Unix(), 10)
	out, err := exec.Command("systemd-run", "--unit="+unit, "--collect", "--quiet",
		"--description=NeoTunnel: undo the upgrade to "+want+" if it doesn't come back",
		Previous(exe), "upgrade-watch", "--version", want, "--exe", exe).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var confirmOnce sync.Once

// Confirm records that this version has answered its main server, which
// is what the watchdog waits for. The agent calls it on every request from
// a main server; only the first does anything.
func Confirm() {
	confirmOnce.Do(func() {
		os.WriteFile(okPath(), []byte(version.Version), 0o600)
	})
}

// Failed is the version that was rolled back here and when ("" = none).
type Failed struct {
	Version string `json:"version"`
	At      int64  `json:"at"`
}

// LastFailed reads the last rollback.
func LastFailed() Failed {
	var f Failed
	if b, err := os.ReadFile(failedPath()); err == nil {
		json.Unmarshal(b, &f)
	}
	return f
}

// RunWatch is the watchdog: it waits for version want to confirm, and puts
// the binary it runs from (the kept old one) back at exe when it doesn't.
func RunWatch(want, exe string, deadline time.Duration) error {
	start := time.Now()
	for time.Since(start) < deadline {
		time.Sleep(3 * time.Second)
		fi, err := os.Stat(okPath())
		if err != nil || fi.ModTime().Before(start) {
			continue
		}
		if b, _ := os.ReadFile(okPath()); strings.TrimSpace(string(b)) == want {
			log.Printf("upgrade to %s confirmed", want)
			return nil
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	log.Printf("%s did not answer its main server in %s: putting the previous version back", want, deadline)
	in, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	tmp := exe + ".rollback"
	if err := os.WriteFile(tmp, in, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		return err
	}
	b, _ := json.Marshal(Failed{Version: want, At: time.Now().Unix()})
	os.WriteFile(failedPath(), b, 0o600)
	return exec.Command("systemctl", "restart", "neotunnel").Run()
}
