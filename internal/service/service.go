// Package service installs NeoTunnel as a systemd service.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	Name     = "neotunnel"
	unitPath = "/etc/systemd/system/neotunnel.service"
)

const unit = `[Unit]
Description=NeoTunnel tunnel manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s daemon
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Installed() bool {
	_, err := os.Stat(unitPath)
	return err == nil
}

// Install writes the unit for the running binary and (re)starts it.
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	want := fmt.Sprintf(unit, exe)
	if cur, _ := os.ReadFile(unitPath); string(cur) != want {
		if err := os.WriteFile(unitPath, []byte(want), 0o644); err != nil {
			return err
		}
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
	}
	if err := systemctl("enable", Name); err != nil {
		return err
	}
	return systemctl("restart", Name)
}

// Restart restarts the service (e.g. to pick up a new panel port).
func Restart() error { return systemctl("restart", Name) }

// Reload asks a running daemon to reconcile now.
func Reload() { exec.Command("systemctl", "kill", "-s", "HUP", Name).Run() }

func Uninstall() error {
	systemctl("disable", "--now", Name)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}
