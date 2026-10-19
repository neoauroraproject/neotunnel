package forward

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
)

func TestFetchBuild(t *testing.T) {
	st := &config.State{Self: "hub", Tunnels: []config.Tunnel{{
		Name: "t1", Kind: config.KindGRE, Net4: "10.77.0.0/30", Secret: config.NewSecret(),
		Sides: [2]config.Side{{Node: "hub", Endpoint: "192.0.2.1"}, {Node: "edge", Endpoint: "192.0.2.2"}},
	}}, Fetch: &config.Fetch{On: true, Tunnel: "t1", Groups: []string{"github"}, Domains: []string{"example.org"}, Strict: true}}
	rs, errs := Build(st, nil, []Link{{Name: "nt-t1", Tunnel: "t1"}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Relays) != 1 || rs.Fetch == nil || rs.Fetch.All {
		t.Fatalf("relays %+v fetch %+v", rs.Relays, rs.Fetch)
	}
	r := rs.Relays[0]
	if r.Port != config.FetchPort || r.Socks || !r.FetchStrict || !strings.Contains(r.FetchSites, "github.com") ||
		!strings.Contains(r.FetchSites, "example.org") || r.Via.Exit.String() != "10.77.0.2:7712" {
		t.Errorf("relay %+v", r)
	}
	s := FetchScript(rs.Fetch)
	for _, want := range []string{"meta mark 0x4e54f001 return", `socket cgroupv2 level 1 "user.slice" tcp dport { 80, 443 } redirect to :7713`,
		"fib daddr type local return", `oifname { "lo", "nt-*" } return`} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if s := FetchScript(&FetchRedirect{All: true}); strings.Contains(s, "cgroupv2") {
		t.Errorf("all programs: %s", s)
	}
	if s := FetchScript(nil); strings.Contains(s, "redirect") || !strings.Contains(s, "delete table inet neotunnel_fetch") {
		t.Errorf("off: %s", s)
	}
	st.Fetch.On = false
	if rs, _ := Build(st, nil, nil); rs.Fetch != nil || len(rs.Relays) != 0 {
		t.Error("off must build nothing")
	}
}
