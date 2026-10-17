package engine

import (
	"testing"

	"neotunnel/internal/config"
)

func TestActiveDropsUnusedCarriers(t *testing.T) {
	ts := []config.Tunnel{
		{Name: "base"},
		{Name: "a", Over: "base", Disabled: true},
		{Name: "b", Over: "base"},
		{Name: "solo-base"},
		{Name: "solo", Over: "solo-base", Disabled: true},
	}
	names := func(ts []config.Tunnel) map[string]bool {
		m := map[string]bool{}
		for _, t := range ts {
			m[t.Name] = true
		}
		return m
	}
	got := names(Active(ts))
	for n, want := range map[string]bool{"base": true, "a": false, "b": true, "solo-base": false, "solo": false} {
		if got[n] != want {
			t.Errorf("%s active = %v, want %v", n, got[n], want)
		}
	}
	SetBlocked(map[string]bool{"b": true})
	defer SetBlocked(nil)
	if got := names(Active(ts)); got["base"] || got["b"] {
		t.Errorf("blocked b should take base down too: %v", got)
	}
}
