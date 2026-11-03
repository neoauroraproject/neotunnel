package version

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.8.16", "v0.8.15", true}, {"v0.10.0", "v0.9.9", true}, {"v0.8.15", "v0.8.15", false},
		{"v0.8.14", "v0.8.15", false}, {"dev", "v0.8.15", false}, {"v0.8.16", "dev", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
