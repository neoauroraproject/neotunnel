//go:build unix

package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lock() (func(), error) { return flock(".lock") }

// LockApply serializes applying the configuration to the kernel across
// processes (the daemon, the CLI, the agent): each applier then reads and
// applies the latest saved state, and none undoes another with an older one.
func LockApply() (func(), error) { return flock(".apply.lock") }

func flock(name string) (func(), error) {
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(Dir, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}
