package events

import (
	"testing"

	"neotunnel/internal/config"
)

func TestAddOfTrim(t *testing.T) {
	config.Dir = t.TempDir()
	Add(Event{Tunnel: "t1", Kind: Up})
	Add(Event{Tunnel: "t2", Kind: Up})
	Add(Event{Tunnel: "t1", Kind: Health, From: "ok", To: "bad", Loss: 95})
	got := Of("t1", 10)
	if len(got) != 2 || got[0].Kind != Health || got[0].Loss != 95 || got[1].Kind != Up || got[0].At == 0 {
		t.Fatalf("events %+v", got)
	}
	if got := Of("t1", 1); len(got) != 1 || got[0].Kind != Health {
		t.Fatalf("limit: %+v", got)
	}
	if len(Of("none", 5)) != 0 {
		t.Fatal("events for an unknown tunnel")
	}
	for range keepLines + 2000 {
		Add(Event{Tunnel: "t3", Kind: Changed, Detail: "a long enough detail to grow the file past its limit quickly........................................................................................................................................................................................................................................................................................................................................................"})
	}
	Trim()
	if n := len(Of("t3", keepLines*2)); n != keepLines {
		t.Fatalf("kept %d lines, want %d", n, keepLines)
	}
}

func TestClass(t *testing.T) {
	for g, want := range map[string]string{"ok": "ok", "degraded": "ok", "bad": "bad", "down": "down", "": ""} {
		if Class(g) != want {
			t.Errorf("Class(%q) = %q", g, Class(g))
		}
	}
}
