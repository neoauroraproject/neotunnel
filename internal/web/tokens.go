package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"neotunnel/internal/config"
)

// API tokens: a program (a sales bot, a script) calls the same API the
// panel does, with "Authorization: Bearer nt_…" instead of a session. A
// token never reaches what only the panel's owner should: its password and
// settings, backups (they hold every key), tokens, the Telegram bot, the
// firewall, updates, and the commands and keys that set up a server.

// tokenPrefix starts every token, so a leaked one is easy to search for.
const tokenPrefix = "nt_"

// TokenView is a token as the panel lists it.
type TokenView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used,omitempty"`
}

// panelOnly lists the API a token never reaches, by route pattern (without
// the method); a prefix ends with "*".
var panelOnly = []string{
	"/api/settings/*", "/api/backup*", "/api/tokens*", "/api/telegram*", "/api/guard*",
	"/api/update", "/api/spot*", "/api/safe*", "/api/peers/{name}/setup", "/api/tunnels/{name}/mikrotik",
	"/api/system/restart", "/api/logout",
}

// tokenAllows says whether a token of scope may call the route pattern
// ("POST /api/forwards").
func tokenAllows(scope, pattern string) bool {
	method, path, _ := strings.Cut(pattern, " ")
	for _, p := range panelOnly {
		if p == path || strings.HasSuffix(p, "*") && strings.HasPrefix(path, strings.TrimSuffix(p, "*")) {
			return false
		}
	}
	switch scope {
	case config.ScopeFull:
		return true
	case config.ScopePorts:
		return method == http.MethodGet || strings.HasPrefix(path, "/api/forwards") || path == "/api/traffic/reset"
	case config.ScopeRead:
		return method == http.MethodGet
	}
	return false
}

// tokens keeps the saved tokens a few seconds, and when each was last used.
type tokens struct {
	mu     sync.Mutex
	list   []config.APIToken
	loaded time.Time
	used   map[string]int64
}

// check finds the token of a request's Authorization header; ok is false
// when the header holds no valid token.
func (t *tokens) check(r *http.Request) (config.APIToken, bool) {
	h := r.Header.Get("Authorization")
	tok, found := strings.CutPrefix(h, "Bearer ")
	if !found || !strings.HasPrefix(tok, tokenPrefix) {
		return config.APIToken{}, false
	}
	sum := sha256.Sum256([]byte(tok))
	want := hex.EncodeToString(sum[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.loaded) > 3*time.Second {
		t.list = nil
		if st, err := config.Load(); err == nil && st.Panel != nil {
			t.list = st.Panel.Tokens
		}
		t.loaded = time.Now()
	}
	for _, k := range t.list {
		if subtle.ConstantTimeCompare([]byte(k.Hash), []byte(want)) == 1 {
			if t.used == nil {
				t.used = map[string]int64{}
			}
			t.used[k.ID] = time.Now().Unix()
			return k, true
		}
	}
	return config.APIToken{}, false
}

// views lists the tokens for the panel.
func (t *tokens) views() ([]TokenView, error) {
	st, err := config.Load()
	if err != nil {
		return nil, err
	}
	out := []TokenView{}
	if st.Panel == nil {
		return out, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range st.Panel.Tokens {
		out = append(out, TokenView{ID: k.ID, Name: k.Name, Scope: k.Scope, Created: k.Created, LastUsed: t.used[k.ID]})
	}
	return out, nil
}

// add makes a token and returns it: the only time it is shown.
func (t *tokens) add(name, scope string) (string, TokenView, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return "", TokenView{}, fmt.Errorf("give the token a name of up to 60 characters")
	}
	if !slices.Contains([]string{config.ScopeRead, config.ScopePorts, config.ScopeFull}, scope) {
		return "", TokenView{}, fmt.Errorf("unknown access %q (read, ports, full)", scope)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", TokenView{}, err
	}
	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	k := config.APIToken{ID: tok[len(tokenPrefix) : len(tokenPrefix)+8], Name: name, Hash: hex.EncodeToString(sum[:]), Scope: scope, Created: time.Now().Unix()}
	_, err := config.Update(func(s *config.State) error {
		if s.Panel == nil {
			return fmt.Errorf("the panel isn't set up")
		}
		if len(s.Panel.Tokens) >= 50 {
			return fmt.Errorf("50 tokens at most; remove one first")
		}
		s.Panel.Tokens = append(s.Panel.Tokens, k)
		return nil
	})
	if err != nil {
		return "", TokenView{}, err
	}
	t.forget()
	return tok, TokenView{ID: k.ID, Name: k.Name, Scope: k.Scope, Created: k.Created}, nil
}

// remove deletes a token; programs using it are refused at once.
func (t *tokens) remove(id string) error {
	_, err := config.Update(func(s *config.State) error {
		if s.Panel == nil {
			return fmt.Errorf("no token %q", id)
		}
		n := len(s.Panel.Tokens)
		s.Panel.Tokens = slices.DeleteFunc(s.Panel.Tokens, func(k config.APIToken) bool { return k.ID == id })
		if len(s.Panel.Tokens) == n {
			return fmt.Errorf("no token %q", id)
		}
		return nil
	})
	t.forget()
	return err
}

func (t *tokens) forget() {
	t.mu.Lock()
	t.loaded = time.Time{}
	t.mu.Unlock()
}
