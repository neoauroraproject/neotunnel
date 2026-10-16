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
	RxPkts  uint64
	TxPkts  uint64
}

// Finding is one thing Doctor noticed: a problem, something it cleaned up, or
// an OK note.
type Finding struct {
	OK      bool // true = healthy, false = a problem or a cleanup
	Fixed   bool // true = Doctor removed a leftover
	Message string
}
