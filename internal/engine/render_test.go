package engine

import (
	"net/netip"
	"testing"

	"neotunnel/internal/config"
)

// sixToFour builds the default quick stack between iran (master) and kharej.
func sixToFour(t *testing.T) *config.State {
	t.Helper()
	st := &config.State{Self: "iran", Address: "198.51.100.1"}
	st.Peers = []config.Peer{{Name: "kharej", Address: "203.0.113.10"}}
	sit := config.Tunnel{Name: "t1-6", Kind: config.KindSIT, Sides: [2]config.Side{
		{Node: "iran", Endpoint: "198.51.100.1"}, {Node: "kharej", Endpoint: "203.0.113.10"}}}
	top := config.Tunnel{Name: "t1", Kind: config.KindIPIP6, Over: "t1-6", Sides: [2]config.Side{{Node: "iran"}, {Node: "kharej"}}}
	for _, tn := range []*config.Tunnel{&sit, &top} {
		if err := st.Allocate(tn); err != nil {
			t.Fatal(err)
		}
		st.Tunnels = append(st.Tunnels, *tn)
	}
	return st
}

func fixedSource(a string) Resolver {
	return func(netip.Addr) (netip.Addr, error) { return netip.MustParseAddr(a), nil }
}

func TestRenderSixToFourBothSides(t *testing.T) {
	st := sixToFour(t)

	master, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(master) != 2 || master[0].Name != "nt-t1-6" || master[1].Name != "nt-t1" {
		t.Fatalf("want carrier then top, got %+v", master)
	}
	sit, top := master[0], master[1]
	if sit.MTU != 1480 || top.MTU != 1440 {
		t.Errorf("mtu: sit %d top %d, want 1480 1440", sit.MTU, top.MTU)
	}
	if sit.Remote.String() != "203.0.113.10" || sit.Local.String() != "198.51.100.1" {
		t.Errorf("sit endpoints %s -> %s", sit.Local, sit.Remote)
	}
	if !top.Local.Is6() || top.Parent != "nt-t1-6" {
		t.Errorf("ipip6 must ride on the sit IPv6 addresses: %+v", top)
	}

	// The peer receives the same definitions and renders the mirror image.
	ts, _ := st.ForPeer("kharej")
	peer := &config.State{Self: "kharej", Managed: &config.Managed{Self: "kharej", Tunnels: ts}}
	far, errs := Render(peer, fixedSource("203.0.113.10"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if far[0].Local != sit.Remote || far[0].Remote != sit.Local {
		t.Errorf("peer sit not mirrored: %s -> %s", far[0].Local, far[0].Remote)
	}
	if far[1].Local != top.Remote || far[1].Remote != top.Local {
		t.Errorf("peer ipip6 not mirrored: %s -> %s", far[1].Local, far[1].Remote)
	}
	if far[1].Addrs[0].Addr() == top.Addrs[0].Addr() {
		t.Errorf("both ends got the same inner address %s", top.Addrs[0])
	}
}

func TestHashIgnoresMTUAndAddrs(t *testing.T) {
	st := sixToFour(t)
	a, _ := Render(st, fixedSource("198.51.100.1"))
	st.Tunnels[1].MTU = 1400
	b, _ := Render(st, fixedSource("198.51.100.1"))
	if a[1].Hash() != b[1].Hash() {
		t.Error("MTU change must not force a tunnel rebuild")
	}
	if b[1].MTU != 1400 {
		t.Errorf("explicit MTU not used: %d", b[1].MTU)
	}
}

func TestRenderRejectsWrongUnderlay(t *testing.T) {
	st := &config.State{Self: "a"}
	st.Tunnels = []config.Tunnel{{Name: "x", Kind: config.KindIP6GRE, Sides: [2]config.Side{
		{Node: "a"}, {Node: "b", Endpoint: "203.0.113.10"}}}}
	if _, errs := Render(st, fixedSource("198.51.100.1")); len(errs) != 1 {
		t.Fatalf("ip6gre over IPv4 should fail, got %v", errs)
	}
}

func TestRenderSkipsOnlyBrokenTunnels(t *testing.T) {
	st := sixToFour(t)
	st.Tunnels = append(st.Tunnels, config.Tunnel{Name: "bad", Kind: config.KindGRE, Sides: [2]config.Side{
		{Node: "iran"}, {Node: "kharej", Endpoint: "not-an-ip"}}})
	specs, errs := Render(st, fixedSource("198.51.100.1"))
	if len(specs) != 2 || len(errs) != 1 {
		t.Fatalf("want 2 good specs and 1 error, got %d / %v", len(specs), errs)
	}
}

func TestVXLANDefaults(t *testing.T) {
	st := &config.State{Self: "a"}
	tn := config.Tunnel{Name: "v", Kind: config.KindVXLAN, Sides: [2]config.Side{{Node: "a"}, {Node: "b", Endpoint: "203.0.113.10"}}}
	st.Allocate(&tn)
	st.Tunnels = []config.Tunnel{tn}
	specs, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	s := specs[0]
	if s.Port != 4789 || s.VNI == 0 || s.MTU != 1450 || len(s.Addrs) != 2 {
		t.Errorf("vxlan defaults wrong: %+v", s)
	}
}
