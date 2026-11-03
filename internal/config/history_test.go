package config

import (
	"reflect"
	"testing"
)

func TestDiffs(t *testing.T) {
	a := Network{
		Tunnels:  []Tunnel{{Name: "t1", Kind: KindGRE}, {Name: "t2", Kind: KindSIT}},
		Forwards: []Forward{{Node: "ir", Proto: "tcp", Ports: "443", Tunnel: "t1"}},
	}
	b := Network{
		Tunnels:  []Tunnel{{Name: "t1", Kind: KindGRE, RateMbit: 100}, {Name: "t3", Kind: KindIPIP}},
		Forwards: []Forward{{Node: "ir", Proto: "tcp", Ports: "443", Tunnel: "t3"}},
		Defaults: &Defaults{Padding: 8},
	}
	want := []Diff{
		{"tunnel", "changed", "t1"}, {"tunnel", "removed", "t2"}, {"tunnel", "added", "t3"},
		{"port", "changed", "tcp/443"},
		{"defaults", "changed", ""},
	}
	if got := Diffs(a, b); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got := Diffs(a, a); len(got) != 0 {
		t.Errorf("no change: %+v", got)
	}
}
