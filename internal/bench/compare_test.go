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
