//go:build !linux

package engine

import (
	"errors"
	"net/netip"
	"time"

	"neotunnel/internal/config"
)

// Stubs so the rest of the code builds and tests on non-Linux dev machines.

var errLinux = errors.New("tunnels require Linux")

func Apply(*config.State) Result                 { return Result{Errors: []error{errLinux}} }
func ApplyBench([]LinkSpec, time.Duration) error { return errLinux }
func RemoveBench([]string)                       {}
func Down() error                                { return errLinux }
func State(string) LinkState                     { return LinkState{} }
func RouteSource(netip.Addr) (netip.Addr, error) { return netip.Addr{}, errLinux }
