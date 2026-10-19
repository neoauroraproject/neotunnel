package config

import (
	"slices"
	"testing"
)

// Two main servers manage this node and both call their tunnel "t1": the
// first keeps the name, the second's gets a local one but keeps its key
// name. A tunnel of the second whose subnet the first already uses waits
// (and is reported busy to the second), while the first never sees it.
func TestSeveralMasters(t *testing.T) {
	a := &Managed{Master: MasterRef{Name: "iran1", PublicKey: "KEYA"}, Self: "tr", Tunnels: []Tunnel{
		{Name: "t1", Kind: KindGRE, Sides: [2]Side{{Node: "iran1"}, {Node: "tr"}}, Net4: "10.77.0.0/30"},
		{Name: "v1", Kind: KindVXLAN, VNI: 7, Sides: [2]Side{{Node: "iran1"}, {Node: "tr"}}, Net4: "10.77.0.4/30"},
	}}
	b := &Managed{Master: MasterRef{Name: "iran2", PublicKey: "KEYB"}, Self: "tr-2", Tunnels: []Tunnel{
		{Name: "t1", Kind: KindGRE, Sides: [2]Side{{Node: "iran2"}, {Node: "tr-2"}}, Net4: "10.77.0.8/30"},
		{Name: "t2", Kind: KindGRE, Sides: [2]Side{{Node: "iran2"}, {Node: "tr-2"}}, Net4: "10.77.0.0/30"},
		{Name: "v1", Kind: KindVXLAN, VNI: 7, Sides: [2]Side{{Node: "iran2"}, {Node: "tr-2"}}, Net4: "10.77.0.12/30"},
	}, Forwards: []Forward{{Node: "iran2", Tunnel: "t1", Ports: "443", Mode: ForwardRelay}}}
	st := &State{Self: "tr", Managed: a, More: []*Managed{b}}

	ts, _ := st.Effective()
	var names []string
	for _, x := range ts {
		names = append(names, x.Name+"/"+x.KeyName())
	}
	t1b := LocalName("t1", "KEYB")
	if !slices.Equal(names, []string{"t1/t1", "v1/v1", t1b + "/t1"}) {
		t.Fatalf("running %v", names)
	}
	if len(t1b) > 10 || t1b == "t1" {
		t.Fatalf("local name %q", t1b)
	}
	if c := st.Conflicts(); len(c) != 2 {
		t.Fatalf("conflicts %v", c)
	}
	if !st.IsSelf("tr-2") || !st.IsSelf("tr") || st.IsSelf("iran2") {
		t.Error("self names")
	}
	if got, ok := st.ManagedFor("KEYB", "t1"); !ok || got.Name != t1b {
		t.Errorf("ManagedFor %+v", got)
	}
	if fs := st.AllForwards(); len(fs) != 1 || fs[0].Tunnel != t1b {
		t.Errorf("forwards %+v", fs)
	}

	// The second master hears what to avoid; the first, what the second has.
	nets, ports := st.Busy("KEYB")
	if !slices.Contains(nets, "10.77.0.0/30") || !slices.Contains(ports, "vni/vxlan/4789/7") || slices.Contains(nets, "10.77.0.8/30") {
		t.Errorf("busy for the second: %v %v", nets, ports)
	}
	nets, _ = st.Busy("KEYA")
	if !slices.Equal(nets, []string{"10.77.0.8/30"}) {
		t.Errorf("busy for the first: %v", nets)
	}

	// On the second master: the peer's report moves a new tunnel's subnet
	// and VNI away from what the peer uses.
	m := &State{Self: "iran2", Peers: []Peer{{Name: "tr-2", Busy: []string{"10.77.0.0/30", "10.77.0.4/30"}, BusyPorts: []string{"vni/vxlan/4789/" + itoa(keyFor("v9")&0xffffff)}}}}
	nt := Tunnel{Name: "v9", Kind: KindVXLAN, Sides: [2]Side{{Node: "iran2"}, {Node: "tr-2"}}}
	if err := m.Allocate(&nt); err != nil {
		t.Fatal(err)
	}
	if nt.Net4 != "10.77.0.8/30" || nt.VNI == keyFor("v9")&0xffffff {
		t.Errorf("allocated %s vni %d", nt.Net4, nt.VNI)
	}
	if nets, port, vni := m.PeerClash(nt); nets || port || vni {
		t.Error("allocation still clashes")
	}
}

func itoa(v uint32) string {
	var b []byte
	for {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
		if v == 0 {
			return string(b)
		}
	}
}
