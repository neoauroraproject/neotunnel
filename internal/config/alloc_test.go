package config

import "testing"

func TestAllocateUniqueSubnets(t *testing.T) {
	st := &State{Self: "a"}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tn := Tunnel{Name: "t", Kind: KindGRE, Sides: [2]Side{{Node: "a"}, {Node: "b"}}}
		if err := st.Allocate(&tn); err != nil {
			t.Fatal(err)
		}
		if seen[tn.Net4] || seen[tn.Net6] {
			t.Fatalf("subnet reused: %s %s", tn.Net4, tn.Net6)
		}
		seen[tn.Net4], seen[tn.Net6] = true, true
		st.Tunnels = append(st.Tunnels, tn)
	}
	if st.Tunnels[0].Net4 != "10.77.0.0/30" || st.Tunnels[1].Net4 != "10.77.0.4/30" {
		t.Errorf("unexpected first subnets %s %s", st.Tunnels[0].Net4, st.Tunnels[1].Net4)
	}
	if st.Tunnels[0].Key == 0 {
		t.Error("GRE key must be set")
	}
}

func TestAllocateOnlyWhatKindCarries(t *testing.T) {
	st := &State{}
	sit := Tunnel{Name: "s", Kind: KindSIT}
	ipip := Tunnel{Name: "i", Kind: KindIPIP}
	st.Allocate(&sit)
	st.Allocate(&ipip)
	if sit.Net4 != "" || sit.Net6 == "" {
		t.Errorf("sit carries IPv6 only: %+v", sit)
	}
	if ipip.Net6 != "" || ipip.Net4 == "" {
		t.Errorf("ipip carries IPv4 only: %+v", ipip)
	}
}

func TestInner(t *testing.T) {
	a, _ := Inner("10.77.0.4/30", 0)
	b, _ := Inner("10.77.0.4/30", 1)
	if a.String() != "10.77.0.5/30" || b.String() != "10.77.0.6/30" {
		t.Errorf("got %s %s", a, b)
	}
	c, _ := Inner("fd00:1:2:3::/64", 1)
	if c.String() != "fd00:1:2:3::2/64" {
		t.Errorf("got %s", c)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"t1", "t1-6", "kharej-01"} {
		if ValidName(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-x", "UPPER", "waytoolongname", "a b"} {
		if ValidName(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
