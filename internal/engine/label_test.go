package engine

import (
	"reflect"
	"testing"
)

// A display name never reaches what the kernel or the daemon runs: renaming
// a tunnel must not take it down or reconnect it.
func TestLabelDoesNotChangeRender(t *testing.T) {
	st := sixToFour(t)
	before, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for i := range st.Tunnels {
		st.Tunnels[i].Label = "آلمان اصلی"
	}
	st.Label, st.Peers[0].Label = "ایران", "آلمان"
	after, errs := Render(st, fixedSource("198.51.100.1"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("labels changed the rendered tunnels:\n%+v\n%+v", before, after)
	}
	for i := range before {
		if before[i].Hash() != after[i].Hash() {
			t.Errorf("%s: hash changed", before[i].Name)
		}
	}
}
