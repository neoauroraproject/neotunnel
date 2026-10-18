//go:build !linux

package tun

import (
	"errors"
	"os"
)

const VnetHdrLen = 10

var errLinux = errors.New("user-space tunnels require Linux")

func Open(string) (*os.File, error)     { return nil, errLinux }
func OpenVnet(string) (*os.File, error) { return nil, errLinux }

func OpenQueues(string, int) ([]*os.File, error) { return nil, errLinux }
func SetOffload(*os.File, bool) error            { return errLinux }
