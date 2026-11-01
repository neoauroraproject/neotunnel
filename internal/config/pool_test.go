package config

import (
	"net/netip"
	"testing"
)

func TestAllocateFromPools(t *testing.T) {
	HostNets = func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("172.27.0.0/29")} }
	defer func() { HostNets = func() []netip.Prefix { return nil } }()
	st := &State{Self: "a", ULA48: "fd00:1:2::/48"}

	gre := Tunnel{Name: "g", Kind: KindGRE, Pool4: "10.50.0.0/29", Pool6: "fd50::/62"}
	if err := st.Allocate(&gre); err != nil {
		t.Fatal(err)
	}
	if gre.Net4 != "10.50.0.0/30" || gre.Net6 != "fd50::/64" {
		t.Fatalf("got %s %s", gre.Net4, gre.Net6)
	}
	st.Tunnels = append(st.Tunnels, gre)

	second := Tunnel{Name: "h", Kind: KindGRE, Pool4: "10.50.0.0/29", Pool6: "fd50::/62"}
	if err := st.Allocate(&second); err != nil {
		t.Fatal(err)
	}
	if second.Net4 != "10.50.0.4/30" || second.Net6 != "fd50:0:0:1::/64" {
		t.Fatalf("got %s %s", second.Net4, second.Net6)
	}
	st.Tunnels = append(st.Tunnels, second)

	// A /29 holds two /30s; a third tunnel gets an error, not addresses past it.
	third := Tunnel{Name: "i", Kind: KindIPIP, Pool4: "10.50.0.0/29"}
	if err := st.Allocate(&third); err == nil {
		t.Fatalf("pool should be full: %s", third.Net4)
	}

	// A range the server sits on is skipped.
	host := Tunnel{Name: "j", Kind: KindIPIP, Pool4: "172.27.0.0/16"}
	if err := st.Allocate(&host); err != nil || host.Net4 != "172.27.0.8/30" {
		t.Fatalf("got %s, %v", host.Net4, err)
	}

	// An IPv6 range narrower than a /64 gives /126s, each with its two ends.
	narrow := Tunnel{Name: "k", Kind: KindSIT, Pool6: "fd60::/124"}
	if err := st.Allocate(&narrow); err != nil || narrow.Net6 != "fd60::/126" {
		t.Fatalf("got %s, %v", narrow.Net6, err)
	}
	if a, _ := Inner(narrow.Net6, 1); a.Addr().String() != "fd60::2" {
		t.Fatalf("far inner %s", a)
	}

	// Only IPv6 chosen: IPv4 comes from the default pool.
	v6 := Tunnel{Name: "l", Kind: KindGRE, Pool6: "fd70::/48"}
	if err := st.Allocate(&v6); err != nil || v6.Net4 != "10.77.0.0/30" || v6.Net6 != "fd70::/64" {
		t.Fatalf("got %s %s, %v", v6.Net4, v6.Net6, err)
	}
}

// A range that overlaps a tunnel's subnet without being it is still taken.
func TestAllocateAvoidsOverlaps(t *testing.T) {
	st := &State{Self: "a", ULA48: "fd00:1:2::/48", Tunnels: []Tunnel{{Name: "w", Kind: KindGRE, Net4: "10.77.0.0/29"}}}
	n := Tunnel{Name: "n", Kind: KindIPIP}
	if err := st.Allocate(&n); err != nil || n.Net4 != "10.77.0.8/30" {
		t.Fatalf("got %s, %v", n.Net4, err)
	}
}

func TestPoolChecks(t *testing.T) {
	for _, bad := range []AddressPool{
		{Name: "a"},
		{Name: "a", Net4: "not a range"},
		{Name: "a", Net4: "fd00::/48"},
		{Name: "a", Net4: "10.0.0.0/31"},
		{Name: "a", Net4: "10.0.0.0/7"},
		{Name: "a", Net4: "127.0.0.0/8"},
		{Name: "a", Net4: "224.0.0.0/8"},
		{Name: "a", Net6: "10.0.0.0/8"},
		{Name: "a", Net6: "fe80::/64"},
	} {
		if err := bad.Check(); err == nil {
			t.Errorf("%+v passed", bad)
		}
	}
	p := AddressPool{Name: "a", Net4: " 10.1.2.3/24 ", Net6: "fd00:1::5/64"}
	if err := p.Check(); err != nil || p.Net4 != "10.1.2.0/24" || p.Net6 != "fd00:1::/64" {
		t.Fatalf("%+v %v", p, err)
	}
	if ValidPoolName("دفتر مرکزی") != nil || ValidPoolName("") == nil || ValidPoolName(" a") == nil {
		t.Fatal("pool names")
	}
}

func TestSortForwards(t *testing.T) {
	st := &State{PortOrder: []string{"tcp/443", "udp/53"}}
	fs := []Forward{{Proto: "tcp", Ports: "80"}, {Proto: "udp", Ports: "53"}, {Proto: "tcp", Ports: "8080"}, {Proto: "tcp", Ports: "443"}}
	got := st.SortForwards(fs)
	want := []string{"443", "53", "80", "8080"}
	for i, f := range got {
		if f.Ports != want[i] {
			t.Fatalf("order %+v", got)
		}
	}
	if fs[0].Ports != "80" {
		t.Fatal("changed its input")
	}
}

func TestBackupList(t *testing.T) {
	var f Forward
	f.Tunnel = "a"
	f.SetBackups([]string{"b", "c", "d"})
	if f.Backup != "b" || len(f.MoreBackups) != 2 || !f.Uses("d") || f.Uses("e") || len(f.Paths()) != 4 {
		t.Fatalf("%+v", f)
	}
	f.SetBackups(nil)
	if f.Backup != "" || f.MoreBackups != nil || len(f.Paths()) != 1 {
		t.Fatalf("%+v", f)
	}
}

// A peer that only a later backup goes to still gets the forward.
func TestForPeerAndMastersKnowEveryBackup(t *testing.T) {
	st := &State{Self: "hub", Tunnels: []Tunnel{
		{Name: "t3", Kind: KindGRE, Sides: [2]Side{{Node: "hub"}, {Node: "edge"}}},
	}, Forwards: []Forward{{Node: "hub", Tunnel: "t1", Backup: "t2", MoreBackups: []string{"t3"}, Mode: ForwardRelay, Proto: "tcp", Ports: "443"}}}
	if _, fs := st.ForPeer("edge"); len(fs) != 1 {
		t.Fatalf("edge lacks the forward: %+v", fs)
	}
	m := &Managed{Master: MasterRef{PublicKey: "k"}, Forwards: []Forward{{Node: "x", Tunnel: "t1", Backup: "t2", MoreBackups: []string{"t3"}}}}
	_, fs := m.view(true, nil)
	if fs[0].MoreBackups[0] != LocalName("t3", "k") || fs[0].Backup != LocalName("t2", "k") {
		t.Fatalf("%+v", fs[0])
	}
}
