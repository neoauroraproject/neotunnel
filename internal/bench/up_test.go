package bench

import (
	"errors"
	"testing"
)

func TestWithPortMovesTheCandidate(t *testing.T) {
	var tcp Candidate
	for _, c := range Candidates("hub", "edge", "192.0.2.1", "192.0.2.2") {
		if c.Name == "tcp" {
			tcp = c
		}
	}
	m := tcp.withPort(51234)
	if m.Port != 51234 || m.Tunnels[0].Port != 51234 || m.Title != "TCP 51234" {
		t.Fatalf("moved candidate: port %d, tunnel port %d, title %q", m.Port, m.Tunnels[0].Port, m.Title)
	}
	if tcp.Tunnels[0].Port != BenchPort+3 {
		t.Fatal("withPort changed the original candidate's tunnels")
	}
}

func TestPortTaken(t *testing.T) {
	for _, s := range []string{
		"peer: ntx-tcp: listen on TCP 40420: listen tcp :40420: bind: address already in use",
		"UDP port 40417 is already used by another program; choose another tunnel port",
	} {
		if !portTaken(errors.New(s)) {
			t.Errorf("not seen as a taken port: %s", s)
		}
	}
	if portTaken(errors.New("no traffic passes on this path")) {
		t.Error("a filtered path is not a taken port")
	}
}
