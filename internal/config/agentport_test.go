package config

import "testing"

func TestEnsureAgentPort(t *testing.T) {
	s := &State{Panel: &Panel{Port: 30000}, Forwards: []Forward{{Ports: "20000-59999"}}}
	// Every port in the range is forwarded but one: that one must be picked.
	s.Forwards[0].Ports = "20000-34566,34568-59999"
	if !EnsureAgentPort(s, nil) || s.AgentPort != 34567 {
		t.Fatalf("picked %d, want the only free port 34567", s.AgentPort)
	}
	if s.AgentLegacy {
		t.Error("a new node should not keep listening on 7710")
	}
	if got := s.AgentPorts(); len(got) != 1 || got[0] != 34567 {
		t.Errorf("AgentPorts = %v", got)
	}
	if EnsureAgentPort(s, nil) {
		t.Error("a node with a port got a new one")
	}

	paired := &State{Peers: []Peer{{Name: "edge"}}}
	EnsureAgentPort(paired, func(p int) bool { return p%2 == 0 })
	if paired.AgentPort%2 != 0 || paired.AgentPort < agentPortMin || paired.AgentPort >= agentPortMax {
		t.Errorf("port %d: not free, or out of range", paired.AgentPort)
	}
	if got := paired.AgentPorts(); !paired.AgentLegacy || len(got) != 2 || got[1] != LegacyAgentPort {
		t.Errorf("a node paired on 7710 should keep it: %v", got)
	}

	old := &State{}
	if old.ListenAgentPort() != LegacyAgentPort {
		t.Error("a node without a port listens on 7710")
	}
}
