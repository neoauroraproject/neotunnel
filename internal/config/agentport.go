package config

import (
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
)

// LegacyAgentPort is where every node's agent listened before each got a
// port of its own. A fixed, well-known port is easy to single out on a
// filtered path, so a node now picks a random one once and keeps it.
const LegacyAgentPort = 7710

const (
	agentPortMin = 20000
	agentPortMax = 60000
)

// ListenAgentPort is this node's own agent port (7710 until it has one).
func (s *State) ListenAgentPort() int {
	if s.AgentPort != 0 {
		return s.AgentPort
	}
	return LegacyAgentPort
}

// AgentPorts are the TCP ports this node's agent listens on: its own, and
// 7710 too on a node that was paired there before, for servers that still
// dial it (an older main server, or a peer finishing its pairing).
func (s *State) AgentPorts() []int {
	p := s.ListenAgentPort()
	if s.AgentLegacy && p != LegacyAgentPort {
		return []int{p, LegacyAgentPort}
	}
	return []int{p}
}

// EnsureAgentPort gives a node without one its own random agent port, not
// used by anything NeoTunnel knows of and free right now (free reports
// that); it reports whether it changed s. A node already paired on 7710
// keeps listening there as well (AgentLegacy).
func EnsureAgentPort(s *State, free func(port int) bool) bool {
	if s.AgentPort != 0 {
		return false
	}
	taken := s.usedPorts()
	ok := func(p int) bool { return !taken[p] && (free == nil || free(p)) }
	pick := func(p int) bool {
		s.AgentLegacy = len(s.Peers) > 0 || len(s.Masters()) > 0
		s.AgentPort = p
		return true
	}
	n := agentPortMax - agentPortMin
	for range 64 {
		if p := agentPortMin + rand.IntN(n); ok(p) {
			return pick(p)
		}
	}
	// Nearly everything is taken: walk the range once from a random place.
	start := rand.IntN(n)
	for i := range n {
		if p := agentPortMin + (start+i)%n; ok(p) {
			return pick(p)
		}
	}
	return false
}

// usedPorts are the ports this node's configuration already gives to
// something: the service's other ports, the panel, tunnels and forwards,
// and the comparison's test ports.
func (s *State) usedPorts() map[int]bool {
	used := map[int]bool{LegacyAgentPort: true, 7711: true, 7712: true, FetchPort: true}
	for p := 40417; p <= 40420; p++ {
		used[p] = true
	}
	if s.Panel != nil {
		used[s.Panel.Port] = true
	}
	ts, _ := s.Effective()
	for _, t := range ts {
		used[int(t.Port)] = true
	}
	for _, f := range s.Forwards {
		for _, part := range strings.Split(f.Ports, ",") {
			lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
			a, err := strconv.Atoi(lo)
			if err != nil {
				continue
			}
			b := a
			if isRange {
				if b, err = strconv.Atoi(hi); err != nil {
					continue
				}
			}
			for p := a; p <= b && p-a < 70000; p++ {
				used[p] = true
			}
		}
	}
	return used
}

// TCPPortFree reports whether a TCP port can be listened on right now.
func TCPPortFree(port int) bool {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}
