package engine

import (
	"sync"

	"neotunnel/internal/relay"
)

// Userspace is the daemon's own data plane for what the kernel can't do:
// NeoObfs and EoIP tunnels and PROXY protocol relays. Only the daemon has one; other
// processes (the CLI) leave those parts alone and nudge the daemon instead.
type Userspace interface {
	// SyncLinks runs exactly these NeoObfs and EoIP tunnels (creating their TUN/TAP
	// interfaces, which Apply then configures like any other link).
	SyncLinks(specs []LinkSpec) []error
	SyncRelays(specs []relay.Spec) []error
}

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
