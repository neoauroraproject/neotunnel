package manage

import (
	"net/netip"
	"slices"
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
)

func routeLocal(remote netip.Addr) (netip.Addr, error) {
	return netip.MustParseAddr("198.51.100.1"), nil
}

func gre(name string) config.Tunnel {
	return config.Tunnel{Name: name, Kind: config.KindGRE, Mode: "gre", Net4: "10.77.0.0/30", AutoEncrypt: true, MTUProbe: true,
		Sides: [2]config.Side{{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "198.51.100.2"}}}
}

func withAES(t *testing.T, on bool) {
	was := hasAES
	hasAES = func() bool { return on }
	t.Cleanup(func() { hasAES = was })
}

func TestCryptoRungs(t *testing.T) {
	withAES(t, true)
	st := &config.State{Self: "hub", Tunnels: []config.Tunnel{gre("t1")}}
	want := []string{"", "aes-gcm-128", "aes-gcm-256", "chacha20-poly1305", config.RungObfs}
	if got := CryptoRungs(st, "t1"); !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	// Without AES instructions at either end, ChaCha20 comes first.
	no := false
	st.Peers = []config.Peer{{Name: "edge", AESNI: &no}}
	chacha := []string{"", "chacha20-poly1305", "aes-gcm-128", "aes-gcm-256", config.RungObfs}
	if got := CryptoRungs(st, "t1"); !slices.Equal(got, chacha) {
		t.Fatalf("far end without AES: got %v", got)
	}
	st.Peers = nil
	withAES(t, false)
	if got := CryptoRungs(st, "t1"); !slices.Equal(got, chacha) {
		t.Fatalf("this end without AES: got %v", got)
	}
	withAES(t, true)
	// No room for "-o" in the name, and no NeoObfs under TCP.
	st.Tunnels = []config.Tunnel{gre("name-of-10")}
	if got := CryptoRungs(st, "name-of-10"); got[len(got)-1] == config.RungObfs {
		t.Fatalf("got %v", got)
	}
	tcp := gre("t1")
	tcp.Kind = config.KindTCP
	st.Tunnels = []config.Tunnel{tcp}
	if got := CryptoRungs(st, "t1"); got[len(got)-1] == config.RungObfs {
		t.Fatalf("got %v", got)
	}
}

// Every step of the ladder is a configuration both ends can render, and the
// NeoObfs layer leaves no trace once it is gone.
func TestLadderRenders(t *testing.T) {
	withAES(t, true)
	st := &config.State{Self: "hub", ULA48: "fd00:1:2::/48", Tunnels: []config.Tunnel{gre("t1")}}
	far := func(n string) bool { return n == "edge" }
	before := st.Tunnels[0]
	for _, rung := range CryptoRungs(st, "t1") {
		if err := applyRung(st, "t1", rung); err != nil {
			t.Fatalf("%q: %v", rung, err)
		}
		if got := CryptoRung(st, "t1"); got != rung {
			t.Fatalf("on %q after asking for %q", got, rung)
		}
		for _, isSelf := range []func(string) bool{st.IsSelf, far} {
			specs, errs := engine.RenderTunnels(isSelf, st.Tunnels, routeLocal)
			if len(errs) > 0 {
				t.Fatalf("%q: %v", rung, errs)
			}
			if rung == config.RungObfs {
				if len(specs) != 2 || specs[0].Kind != config.KindObfs || specs[1].Parent != specs[0].Name || specs[1].Encrypt != "" {
					t.Fatalf("obfs step: %+v", specs)
				}
			} else if len(specs) != 1 || specs[0].Encrypt != rung {
				t.Fatalf("%q: %+v", rung, specs)
			}
		}
	}
	top, _ := st.Tunnel("t1")
	if layer, ok := st.Tunnel("t1-o"); !ok || !layer.AutoLayer || layer.Port == 0 || !layer.MTUProbe || top.MTUProbe {
		t.Fatalf("layer %+v, top %+v", layer, top)
	}
	if err := applyRung(st, "t1", ""); err != nil {
		t.Fatal(err)
	}
	top, _ = st.Tunnel("t1")
	if len(st.Tunnels) != 1 || top.Over != "" || top.Sides != before.Sides || !top.MTUProbe || top.Encrypt != "" {
		t.Fatalf("after the layer: %+v", st.Tunnels)
	}
}

func TestLadderOnA6to4Stack(t *testing.T) {
	sit := config.Tunnel{Name: "t1-6", Kind: config.KindSIT, Net6: "fd00:1:2:1::/64", Sides: [2]config.Side{{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "198.51.100.2"}}}
	top := config.Tunnel{Name: "t1", Kind: config.KindIPIP6, Over: "t1-6", Mode: "6to4", AutoEncrypt: true, Net4: "10.77.0.0/30", Sides: [2]config.Side{{Node: "hub"}, {Node: "edge"}}}
	st := &config.State{Self: "hub", ULA48: "fd00:1:2::/48", Tunnels: []config.Tunnel{sit, top}}
	if err := applyRung(st, "t1", "aes-gcm-128"); err != nil {
		t.Fatal(err)
	}
	if b, _ := st.Tunnel("t1-6"); b.Encrypt != "aes-gcm-128" || b.Secret == "" {
		t.Fatalf("encryption belongs on the bottom: %+v", b)
	}
	if err := applyRung(st, "t1", config.RungObfs); err != nil {
		t.Fatal(err)
	}
	if got := Stack(st, "t1"); !slices.Equal(got, []string{"t1-o", "t1-6", "t1"}) {
		t.Fatalf("stack %v", got)
	}
	specs, errs := engine.RenderTunnels(st.IsSelf, st.Tunnels, routeLocal)
	if len(errs) > 0 || len(specs) != 3 {
		t.Fatalf("%v %+v", errs, specs)
	}
	if err := applyRung(st, "t1", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := st.Tunnel("t1-6"); b.Over != "" || b.Sides[1].Endpoint != "198.51.100.2" || b.Encrypt != "" {
		t.Fatalf("bottom after the layer: %+v", b)
	}
}

func TestObfsAESNeedsBothEnds(t *testing.T) {
	yes, no := true, false
	st := &config.State{Self: "hub", Peers: []config.Peer{
		{Name: "ok", AESNI: &yes, ObfsAES: true},
		{Name: "old", AESNI: &yes},
		{Name: "noaes", AESNI: &no, ObfsAES: true},
		{Name: "unknown", ObfsAES: true},
	}}
	withAES(t, true)
	for name, want := range map[string]bool{"ok": true, "old": false, "noaes": false, "unknown": false, "stranger": false} {
		if got := obfsAES(st, name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
	withAES(t, false)
	if obfsAES(st, "ok") {
		t.Error("this server has no AES instructions")
	}
}

// After the far server was upgraded, its old NeoObfs tunnels move to AES; others
// stay as they are.
func TestObfsTunnelsMoveToAES(t *testing.T) {
	yes := true
	ends := [2]config.Side{{Node: "hub"}, {Node: "edge"}}
	st := &config.State{Self: "hub", Peers: []config.Peer{{Name: "edge", AESNI: &yes}}, Tunnels: []config.Tunnel{
		{Name: "o1", Kind: config.KindObfs, Sides: ends},
		{Name: "o2", Kind: config.KindObfs, Sides: ends, ObfsAES: true},
		{Name: "g1", Kind: config.KindGRE, Sides: ends},
		{Name: "o3", Kind: config.KindObfs, Sides: [2]config.Side{{Node: "hub"}, {Node: "other"}}},
	}}
	withAES(t, true)
	if got := obfsToAES(st, "edge"); len(got) != 0 {
		t.Fatalf("moved before edge said it can: %v", got)
	}
	st.Peers[0].ObfsAES = true
	if got := obfsToAES(st, "edge"); !slices.Equal(got, []int{0}) {
		t.Fatalf("got %v", got)
	}
	withAES(t, false)
	if got := obfsToAES(st, "edge"); len(got) != 0 {
		t.Fatalf("moved without AES instructions here: %v", got)
	}
}

func TestPMTUFlags(t *testing.T) {
	if no, ig, err := pmtuFlags(config.KindGRE, true, true); !no || !ig || err != nil {
		t.Fatal(no, ig, err)
	}
	if no, ig, err := pmtuFlags(config.KindSIT, true, false); !no || ig || err != nil {
		t.Fatal(no, ig, err)
	}
	// No outer DF bit to clear: the flags are dropped.
	if no, _, err := pmtuFlags(config.KindIP6GRE, true, false); no || err != nil {
		t.Fatal(no, err)
	}
	if _, _, err := pmtuFlags(config.KindIPIP, true, true); err == nil {
		t.Fatal("IPIP can't ignore DF")
	}
	if err := (Settings{IgnoreDF: true}).validate(); err == nil {
		t.Fatal("ignore-df without PMTUD off")
	}
}

func TestPoolChoice(t *testing.T) {
	st := &config.State{Self: "hub", Pools: []config.AddressPool{{Name: "mine", Net4: "10.9.0.0/24", Net6: "fd09::/48"}}}
	check := func(c PoolChoice, n4, n6 string) {
		t.Helper()
		a, b, err := c.ranges(st)
		if err != nil || a != n4 || b != n6 {
			t.Fatalf("%+v: %s %s %v", c, a, b, err)
		}
	}
	check(PoolChoice{}, "", "")
	check(PoolChoice{Pool: "default"}, "", "")
	check(PoolChoice{Pool: "mine"}, "10.9.0.0/24", "fd09::/48")
	check(PoolChoice{Pool: "172"}, "172.27.0.0/16", "")
	check(PoolChoice{Pool: "mine", Pool4: "10.8.0.0/30"}, "10.8.0.0/30", "")
	st.Defaults = &config.Defaults{Pool: "mine"}
	check(PoolChoice{}, "10.9.0.0/24", "fd09::/48")
	check(PoolChoice{Pool: "default"}, "", "")
	if _, _, err := (PoolChoice{Pool: "nope"}).ranges(st); err == nil {
		t.Fatal("unknown pool")
	}
	if _, _, err := (PoolChoice{Pool4: "fd00::/48"}).ranges(st); err == nil {
		t.Fatal("IPv6 range as IPv4")
	}
	bad := gre("t1")
	bad.Pool4 = "198.51.100.0/24"
	if err := checkPoolAddrs(bad); err == nil {
		t.Fatal("a range with the server's own address")
	}
}
