package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.8.4", "v0.8.3", true},
		{"v0.9.0", "v0.8.12", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.8.10", "v0.8.9", true},
		{"v0.8.3", "v0.8.3", false},
		{"v0.8.2", "v0.8.3", false},
		{"v0.9.0", "dev", false},
		{"latest", "v0.8.3", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, s := range []string{"v0.8.4; rm -rf /", "v0.8", "0.8.4", "v0.8.4 ", ""} {
		if validTag(s) {
			t.Errorf("validTag(%q) = true", s)
		}
	}
	if !validTag("v12.0.3") {
		t.Error("validTag(v12.0.3) = false")
	}
}

func TestSessionHandoff(t *testing.T) {
	prev := handoffFile
	t.Cleanup(func() { handoffFile = prev })
	handoffFile = filepath.Join(t.TempDir(), "sessions.handoff")
	old := &Server{}
	old.init()
	old.sessions["live"] = time.Now().Add(time.Hour)
	old.sessions["gone"] = time.Now().Add(-time.Minute)
	old.handOffSessions()

	next := &Server{}
	next.init()
	if _, err := os.Stat(handoffFile); !os.IsNotExist(err) {
		t.Fatal("the handoff file is still there after a start")
	}
	if len(next.handed) != 1 || next.handed[sessionHash("live")].IsZero() {
		t.Fatalf("handed = %v, want only the live session", next.handed)
	}
	again := &Server{}
	again.init()
	if len(again.handed) != 0 {
		t.Fatal("a second start took the sessions again")
	}
}
