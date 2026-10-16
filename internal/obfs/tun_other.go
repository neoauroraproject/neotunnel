//go:build !linux

package obfs

import (
	"errors"
	"os"
)

func openTUN(string) (*os.File, error) { return nil, errors.New("NeoObfs requires Linux") }
