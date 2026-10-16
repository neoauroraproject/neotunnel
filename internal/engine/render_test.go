package engine

import (
	"net/netip"
	"testing"

	"neotunnel/internal/config"
)

// sixToFour builds the default quick stack between hub (master) and edge.
func sixToFour(t *testing.T) *config.State {
	t.Helper()
	st := &config.State{Self: "hub", Address: "198.51.100.1"}
	st.Peers = []config.Peer{{Name: "edge", Address: "203.0.113.10"}}
	sit := config.Tunnel{Name: "t1-6", Kind: config.KindSIT, Sides: [2]config.Side{
		{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "203.0.113.10"}}}
	top := config.Tunnel{Name: "t1", Kind: config.KindIPIP6, Over: "t1-6", Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}}}
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
	ts, _ := st.ForPeer("edge")
	peer := &config.State{Self: "edge", Managed: &config.Managed{Self: "edge", Tunnels: ts}}
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
		{Node: "hub"}, {Node: "edge", Endpoint: "not-an-ip"}}})
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

func peerTunnel(kind config.Kind, mut func(*config.Tunnel)) *config.State {
	st := &config.State{Self: "a"}
	tn := config.Tunnel{Name: "x", Kind: kind, Sides: [2]config.Side{{Node: "a", Endpoint: "198.51.100.1"}, {Node: "b", Endpoint: "203.0.113.10"}}}
	if mut != nil {
		mut(&tn)
	}
	st.Allocate(&tn)
	st.Tunnels = []config.Tunnel{tn}
	return st
}

func TestEncryptionCostsMTU(t *testing.T) {
	st := peerTunnel(config.KindIPIP, func(tn *config.Tunnel) { tn.Encrypt = "aes-gcm-128" })
	specs, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if specs[0].MTU != 1500-20-config.ESPOverhead || specs[0].Encrypt != "aes-gcm-128" || specs[0].Secret == "" {
		t.Errorf("encrypted ipip: %+v", specs[0])
	}
	st.Tunnels[0].Encrypt = "rot13"
	if _, errs := Render(st, fixedSource("198.51.100.1")); len(errs) != 1 {
		t.Error("unknown cipher accepted")
	}
}

func TestESPKeysMirror(t *testing.T) {
	st := peerTunnel(config.KindGRE, func(tn *config.Tunnel) { tn.Encrypt = "aes-gcm-256" })
	a, _ := Render(st, fixedSource("198.51.100.1"))
	peer := &config.State{Self: "b", Managed: &config.Managed{Self: "b", Tunnels: st.Tunnels}}
	b, _ := Render(peer, fixedSource("203.0.113.10"))
	outA, _ := espFor(a[0], a[0].Side)
	inB, _ := espFor(b[0], 1-b[0].Side)
	if outA.SPI != inB.SPI || string(outA.Key) != string(inB.Key) || len(outA.Key) != 36 {
		t.Errorf("a's outbound SA must be b's inbound SA")
	}
	inA, _ := espFor(a[0], 1-a[0].Side)
	if inA.SPI == outA.SPI {
		t.Error("both directions share an SPI")
	}
	if p, port := outerProto(a[0]); p != 47 || port != 0 {
		t.Errorf("gre outer proto %d/%d", p, port)
	}
}

func TestWireGuardAndObfsDefaults(t *testing.T) {
	st := peerTunnel(config.KindWireGuard, nil)
	specs, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if s := specs[0]; s.Port != WireGuardPort || s.MTU != 1440 || s.Secret == "" {
		t.Errorf("wireguard: %+v", s)
	}
	priv, peer, err := wgKeys(specs[0])
	if err != nil || priv == peer {
		t.Fatalf("wireguard keys: %v", err)
	}
	pub, _ := WireGuardPublicKey(st.Tunnels[0], 1)
	if pub == "" {
		t.Error("no public key")
	}

	st = peerTunnel(config.KindObfs, func(tn *config.Tunnel) { tn.Port = 8443; tn.Padding = 32 })
	specs, errs = Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if s := specs[0]; !s.Userspace || s.MTU != 1500-config.ObfsOverhead-32 {
		t.Errorf("obfs: %+v", s)
	}
}

func TestL2TPv3Ports(t *testing.T) {
	st := peerTunnel(config.KindL2TPv3, nil)
	specs, _ := Render(st, fixedSource("198.51.100.1"))
	if s := specs[0]; s.Port != 0 || s.FOU || s.MTU != 1462 || s.Key == 0 {
		t.Errorf("l2tpv3 over ip: %+v", s)
	}
	st = peerTunnel(config.KindL2TPv3, func(tn *config.Tunnel) { tn.Port = 1701 })
	specs, _ = Render(st, fixedSource("198.51.100.1"))
	if s := specs[0]; s.Port != 1701 || s.FOU || s.MTU != 1450 {
		t.Errorf("l2tpv3 over udp: %+v", s)
	}
	if p, port := outerProto(specs[0]); p != 17 || port != 1701 {
		t.Errorf("l2tpv3/udp outer %d/%d", p, port)
	}
}
