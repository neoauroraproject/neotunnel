package engine

import (
	"errors"
)

type Result struct {
	Specs   []LinkSpec
	Created []string
	Removed []string
	Errors  []error
}

func (r Result) Err() error { return errors.Join(r.Errors...) }

type LinkState struct {
	Exists  bool
	Up      bool
	MTU     int
	RxBytes uint64
	TxBytes uint64
}
