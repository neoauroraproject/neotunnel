package config

import (
	"fmt"
	"math/rand/v2"
	"slices"
)

// Relay connections used to go to one fixed port, 7712, on every server: a
// port that is easy to single out and block on a filtered path. Each tunnel
// now has MaxRelayPorts random ports of its own instead, and the entry
// tries them in order until one connects (remembering the one that did).
// A connection tunnel's listener gets the same, after its own Port.

// NewRelayPorts picks n random TCP ports (20000-59999) for t's relay or
// sessions, none of them used on this server for anything else NeoTunnel
// knows of, nor reported taken by t's far server, and none in skip.
func (s *State) NewRelayPorts(t Tunnel, n int, skip ...uint16) []uint16 {
	taken := s.usedPorts()
	_, busy := s.peerBusy(t)
	var out []uint16
	for range 512 {
		if len(out) == n {
			break
		}
		p := uint16(agentPortMin + rand.IntN(agentPortMax-agentPortMin))
		if taken[int(p)] || busy[fmt.Sprintf("tcp/%d", p)] || slices.Contains(out, p) || slices.Contains(skip, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// EnsureRelayPorts gives t its relay ports when it has none: MaxRelayPorts
// for a packet tunnel, and for a connection tunnel the ones after its Port.
// It reports whether it changed t.
func (s *State) EnsureRelayPorts(t *Tunnel) bool {
	if len(t.RelayPorts) > 0 {
		return false
	}
	n := MaxRelayPorts
	var skip []uint16
	if t.IsStream() && t.Port != 0 {
		n--
		skip = []uint16{t.Port}
	}
	t.RelayPorts = s.NewRelayPorts(*t, n, skip...)
	return len(t.RelayPorts) > 0
}
