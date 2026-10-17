package engine

import (
	"errors"
	"sync"
	"time"

	"neotunnel/internal/relay"
)

// Userspace is the daemon's own data plane for what the kernel can't do:
// NeoObfs, TCP and EoIP tunnels and PROXY protocol relays. Only the daemon
// has one; other processes (the CLI) leave those parts alone and nudge the
// daemon instead.
type Userspace interface {
	// SyncLinks runs exactly these user-space tunnels (creating their TUN/TAP
	// interfaces, which Apply then configures like any other link).
	SyncLinks(specs []LinkSpec) []error
	SyncRelays(specs []relay.Spec) []error
	// StartBench runs a temporary tunnel for a speed test until expiry or
	// StopBench, whichever comes first.
	StartBench(spec LinkSpec, expiry time.Time) error
	StopBench(names []string)
}

// ErrNeedsDaemon: a user-space tunnel was asked of a process without the
// data plane.
var ErrNeedsDaemon = errors.New("this tunnel runs inside the NeoTunnel service; test it from the panel or with the service running")

var (
	usMu      sync.Mutex
	userspace Userspace
)

// SetUserspace registers the daemon's data plane.
func SetUserspace(u Userspace) {
	usMu.Lock()
	defer usMu.Unlock()
	userspace = u
}

func currentUserspace() Userspace {
	usMu.Lock()
	defer usMu.Unlock()
	return userspace
}

// InDaemon reports whether this process runs the user-space data plane.
func InDaemon() bool { return currentUserspace() != nil }
