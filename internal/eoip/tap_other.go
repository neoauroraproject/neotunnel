//go:build !linux

package eoip

import (
	"errors"
	"net"
	"os"
	"syscall"
)

var errLinux = errors.New("EoIP requires Linux")

func openTAP(string, net.HardwareAddr) (*os.File, bool, error) { return nil, false, errLinux }

func filterGRE(syscall.Conn, uint16) error { return errLinux }

func tune(carrier, Config) error { return errLinux }
