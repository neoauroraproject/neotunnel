//go:build !linux

package mtr

import (
	"context"
	"errors"
	"net/netip"
)

// Run needs Linux's raw sockets.
func Run(ctx context.Context, target netip.Addr, rounds int) (Result, error) {
	return Result{}, errors.New("MTR runs on Linux")
}
