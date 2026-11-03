package upgrade

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/version"
)

// The old binary is kept beside the new one; the watchdog stops once the
// new version confirms.
func TestKeepAndConfirm(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	exe := filepath.Join(t.TempDir(), "neotunnel")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := Keep(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Previous(exe)); string(b) != "old" {
		t.Fatalf("kept %q", b)
	}
	defer func(v string) { version.Version = v }(version.Version)
	version.Version = "v9.9.9"
	go func() {
		time.Sleep(500 * time.Millisecond)
		Confirm()
	}()
	if err := RunWatch("v9.9.9", exe, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if LastFailed().Version != "" {
		t.Error("a confirmed upgrade recorded as failed")
	}
}

// A version that never confirms is replaced by the binary the watchdog
// runs from (the old one), and recorded as rolled back.
func TestRollback(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	exe := filepath.Join(t.TempDir(), "neotunnel")
	os.WriteFile(exe, []byte("broken new version"), 0o755)
	RunWatch("v9.9.9", exe, 4*time.Second) // systemctl may be missing here
	self, _ := os.Executable()
	a, _ := os.ReadFile(self)
	b, _ := os.ReadFile(exe)
	if len(b) != len(a) {
		t.Errorf("the old binary isn't back (%d bytes, want %d)", len(b), len(a))
	}
	if f := LastFailed(); f.Version != "v9.9.9" || f.At == 0 {
		t.Errorf("rollback not recorded: %+v", f)
	}
}
