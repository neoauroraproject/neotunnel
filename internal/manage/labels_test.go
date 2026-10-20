package manage

import (
	"strings"
	"testing"
)

func TestCleanLabel(t *testing.T) {
	for _, c := range []struct{ in, id, want string }{
		{"  آلمان   هتزنر ", "germany", "آلمان هتزنر"},
		{"germany", "germany", ""}, // the ID itself: stored empty
		{"", "t1", ""},
		{"6to4 Germany #1", "t1", "6to4 Germany #1"},
	} {
		got, err := CleanLabel(c.in, c.id)
		if err != nil || got != c.want {
			t.Errorf("CleanLabel(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := CleanLabel(strings.Repeat("ا", MaxLabel+1), "t1"); err == nil {
		t.Error("an over-long label was accepted")
	}
	if _, err := CleanLabel("a\x00b", "t1"); err == nil {
		t.Error("a control character was accepted")
	}
}
