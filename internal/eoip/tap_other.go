//go:build !linux

package eoip

import (
	"errors"
	"net"
	"os"
)

var errLinux = errors.New("EoIP requires Linux")

func openTAP(string, net.HardwareAddr) (*os.File, error) { return nil, errLinux }

func tune(carrier, Config) error { return errLinux }
