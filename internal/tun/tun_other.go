//go:build !linux

package tun

import (
	"errors"
	"os"
)

func Open(string) (*os.File, error) { return nil, errors.New("user-space tunnels require Linux") }
