package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"neotunnel/internal/config"
)

// A token reaches what its scope allows, and never the panel's own
// settings, backups, tokens or secrets.
func TestTokenAllows(t *testing.T) {
	for _, c := range []struct {
		scope, pattern string
		ok             bool
	}{
		{"read", "GET /api/overview", true},
		{"read", "GET /api/forwards/clients", true},
		{"read", "POST /api/forwards/plan", false},
		{"ports", "POST /api/forwards/plan", true},
		{"ports", "POST /api/forwards/renew", true},
		{"ports", "POST /api/forwards", true},
		{"ports", "POST /api/tunnels", false},
		{"ports", "DELETE /api/tunnels/{name}", false},
		{"full", "POST /api/tunnels", true},
		{"full", "DELETE /api/peers/{name}", true},
		{"full", "GET /api/backup", false},
		{"full", "POST /api/backup/restore", false},
		{"full", "POST /api/settings/password", false},
		{"full", "GET /api/tokens", false},
		{"full", "POST /api/tokens", false},
		{"full", "GET /api/peers/{name}/setup", false},
		{"full", "GET /api/tunnels/{name}/mikrotik", false},
		{"full", "POST /api/telegram", false},
		{"full", "POST /api/guard", false},
		{"full", "POST /api/update", false},
		{"nonsense", "GET /api/overview", false},
	} {
		if got := tokenAllows(c.scope, c.pattern); got != c.ok {
			t.Errorf("%s %s: %v, want %v", c.scope, c.pattern, got, c.ok)
		}
	}
}

// A token works from when it is made until it is removed; only its hash
// is saved.
func TestTokens(t *testing.T) {
	defer func(d string) { config.Dir = d }(config.Dir)
	config.Dir = t.TempDir()
	config.Update(func(s *config.State) error { s.Panel = &config.Panel{User: "admin"}; return nil })
	var k tokens
	if _, _, err := k.add("bot", "root"); err == nil {
		t.Error("unknown scope taken")
	}
	tok, v, err := k.add(" sales bot ", config.ScopePorts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "nt_") || v.Name != "sales bot" || !strings.HasPrefix(tok[3:], v.ID) {
		t.Fatalf("token %q view %+v", tok, v)
	}
	st, _ := config.Load()
	if b, _ := json.Marshal(st); strings.Contains(string(b), tok) {
		t.Error("the token itself is saved")
	}
	req := func(h string) *http.Request {
		r, _ := http.NewRequest("GET", "/api/overview", nil)
		r.Header.Set("Authorization", h)
		return r
	}
	if got, ok := k.check(req("Bearer " + tok)); !ok || got.Scope != config.ScopePorts {
		t.Errorf("valid token refused: %+v", got)
	}
	for _, bad := range []string{"Bearer " + tok + "x", "Bearer nt_nothing", tok, ""} {
		if _, ok := k.check(req(bad)); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if vs, _ := k.views(); len(vs) != 1 || vs[0].LastUsed == 0 {
		t.Errorf("views %+v", vs)
	}
	if err := k.remove(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := k.check(req("Bearer " + tok)); ok {
		t.Error("removed token still works")
	}
}
