//go:build !linux

package daemon

import "context"

func watchNet(context.Context, func()) {}
