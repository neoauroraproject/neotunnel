package config

import (
	"slices"
	"testing"
)

func TestFetchSites(t *testing.T) {
	f := Fetch{Groups: []string{"github"}, Domains: []string{"https://Download.Docker.com/linux/", "*.example.org", "github.com"}}
	got := f.Sites()
	for _, want := range []string{"github.com", "githubusercontent.com", "download.docker.com", "example.org"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if n := len(got); n != 7 {
		t.Errorf("duplicates kept: %v", got)
	}
	for _, bad := range []string{"localhost", "a..b", "-x.com", "x_y.com"} {
		if ValidDomain(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A peer learns that the downloads leave through it from a proxy forward
// on the tunnel, which only the tunnel's far server gets.
func TestFetchForPeer(t *testing.T) {
	st := &State{Self: "ir", Tunnels: []Tunnel{
		{Name: "t1", Sides: [2]Side{{Node: "ir"}, {Node: "de"}}},
		{Name: "t2", Sides: [2]Side{{Node: "ir"}, {Node: "nl"}}},
	}, Fetch: &Fetch{On: true, Tunnel: "t1", Groups: []string{"github"}}}
	_, fs := st.ForPeer("de")
	if len(fs) != 1 || !fs[0].Fetch || fs[0].ModeOf() != ForwardSocks || fs[0].Node != "ir" || fs[0].Tunnel != "t1" {
		t.Fatalf("de: %+v", fs)
	}
	if _, fs := st.ForPeer("nl"); len(fs) != 0 {
		t.Errorf("nl: %+v", fs)
	}
	st.Fetch.On = false
	if _, fs := st.ForPeer("de"); len(fs) != 0 {
		t.Errorf("off: %+v", fs)
	}
	// It never shows up as this server's own forward.
	st.Fetch.On = true
	if _, fs := st.Effective(); len(fs) != 0 {
		t.Errorf("effective: %+v", fs)
	}
}
