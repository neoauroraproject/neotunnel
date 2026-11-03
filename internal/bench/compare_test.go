package bench

import "testing"

func TestWavesKeepConflictsApart(t *testing.T) {
	cs := Candidates("a", "b", "198.51.100.1", "203.0.113.2")
	ws := waves(cs)
	seen := 0
	for _, w := range ws {
		keys := map[string]bool{}
		for _, c := range w {
			seen++
			if k := conflictKey(c); k != "" {
				if keys[k] {
					t.Errorf("wave holds two %s candidates", k)
				}
				keys[k] = true
			}
		}
	}
	if seen != len(cs) {
		t.Fatalf("waves hold %d of %d candidates", seen, len(cs))
	}
	// 6to4 and 6to4-gre both use a raw SIT carrier: two waves at least.
	if len(ws) < 2 {
		t.Errorf("want at least 2 waves, got %d", len(ws))
	}
	if len(ws[0]) < len(cs)-2 {
		t.Errorf("first wave should take nearly everything, got %d of %d", len(ws[0]), len(cs))
	}
}

// The connection tunnels are compared too: each on a test port of its own,
// with a secret, the far server listening, measured over the sessions of
// the temporary tunnel both servers start under the same name.
func TestStreamCandidates(t *testing.T) {
	ports := map[uint16]string{}
	found := 0
	for _, c := range Candidates("hub", "edge", "192.0.2.1", "192.0.2.2") {
		if c.Port != 0 {
			if o, ok := ports[c.Port]; ok {
				t.Errorf("%s and %s share test port %d", o, c.Name, c.Port)
			}
			ports[c.Port] = c.Name
		}
		if !c.stream() {
			if tg := candidateTarget(c); tg.stream != "" {
				t.Errorf("%s: packet tunnel measured over sessions", c.Name)
			}
			continue
		}
		found++
		top := c.Tunnels[0]
		if top.Secret == "" || top.Port == 0 || top.Listener() != 1 {
			t.Errorf("%s: secret %t, port %d, listener %d", c.Name, top.Secret != "", top.Port, top.Listener())
		}
		if tg := candidateTarget(c); tg.stream != "ntx-"+top.Name {
			t.Errorf("%s: probes go to %+v", c.Name, tg)
		}
	}
	if found != 2 {
		t.Fatalf("want TCP + mux and TLS + mux among the candidates, found %d", found)
	}
}
