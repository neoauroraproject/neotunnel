package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
	"neotunnel/internal/traffic"
	"neotunnel/internal/web"
)

// stub is a Backend that only knows one login; the tests exercise the HTTP
// layer (paths, sessions, CSRF, lockout), not the operations.
type stub struct{}

func (stub) Overview() (web.Overview, error)                            { return web.Overview{}, nil }
func (stub) AddPeer(string, string) (fleet.Setup, error)                { return fleet.Setup{}, nil }
func (stub) RemovePeer(string) (manage.Change, error)                   { return manage.Change{}, nil }
func (stub) PeerSetup(string) (fleet.Setup, error)                      { return fleet.Setup{}, nil }
func (stub) SyncPeers([]string) []manage.PeerSync                       { return nil }
func (stub) UpgradePeer(context.Context, string) (string, error)        { return "", nil }
func (stub) CreateTunnel(manage.TunnelRequest) (manage.Change, error)   { return manage.Change{}, nil }
func (stub) RemoveTunnel(string) (manage.Change, error)                 { return manage.Change{}, nil }
func (stub) AddForward(manage.ForwardRequest) (manage.Change, error)    { return manage.Change{}, nil }
func (stub) UpdateForward(manage.ForwardRequest) (manage.Change, error) { return manage.Change{}, nil }
func (stub) RandomPort(string) (uint16, error)                          { return 40000, nil }

func (stub) RestartTunnel(context.Context, string) error { return nil }

func (stub) SetLimits(string, manage.Limits) (manage.Change, error) { return manage.Change{}, nil }

func (stub) Check(context.Context, manage.TunnelRequest, bool, bool) ([]bench.Result, error) {
	return nil, nil
}

func (stub) UpdateTunnel(string, manage.Settings, uint16) (manage.Change, error) {
	return manage.Change{}, nil
}
func (stub) SetDefaults(config.Defaults) error { return nil }
func (stub) ProbeMTU(context.Context, string) (web.MTUReport, error) {
	return web.MTUReport{}, nil
}
func (stub) Traffic() map[string]traffic.Summary                 { return nil }
func (stub) ResetTraffic(string) error                           { return nil }
func (stub) System() (web.SystemInfo, error)                     { return web.SystemInfo{}, nil }
func (stub) Boost() error                                        { return nil }
func (stub) Backup() ([]byte, error)                             { return nil, nil }
func (stub) SetUser(string) error                                { return nil }
func (stub) RemoveForward(string, string) (manage.Change, error) { return manage.Change{}, nil }
func (stub) SetNode(string, string) error                        { return nil }
func (stub) TestTunnel(context.Context, string, func(string), func(string, float64)) ([]bench.Result, error) {
	return nil, nil
}
func (stub) Bench(context.Context, string, []string, func(string), func(string, float64), func(bench.Result)) ([]bench.Result, error) {
	return nil, nil
}
func (stub) Login(user, pass string) bool        { return user == "admin" && pass == "right-password" }
func (stub) ChangePassword(string, string) error { return nil }
func (stub) SetPanelPort(int) (string, error)    { return "", nil }

func newPanel(t *testing.T) http.Handler {
	t.Helper()
	return (&web.Server{Backend: stub{}, Path: "/secret"}).Handler()
}

func do(h http.Handler, method, path, body string, cookies []*http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:5555"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, h http.Handler) []*http.Cookie {
	t.Helper()
	rec := do(h, "POST", "/secret/api/login", `{"user":"admin","password":"right-password"}`, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	return rec.Result().Cookies()
}

func TestSecretPath(t *testing.T) {
	h := newPanel(t)
	if rec := do(h, "GET", "/", "", nil, nil); rec.Code != 404 {
		t.Errorf("root: %d, want 404", rec.Code)
	}
	if rec := do(h, "GET", "/secret/", "", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "app.js") {
		t.Errorf("panel page: %d", rec.Code)
	}
	if rec := do(h, "GET", "/secret/assets/app.css", "", nil, nil); rec.Code != 200 {
		t.Errorf("assets: %d", rec.Code)
	}
}

func TestAPIRequiresLogin(t *testing.T) {
	h := newPanel(t)
	if rec := do(h, "GET", "/secret/api/overview", "", nil, nil); rec.Code != 401 {
		t.Fatalf("overview without session: %d", rec.Code)
	}
	c := login(t, h)
	if rec := do(h, "GET", "/secret/api/overview", "", c, nil); rec.Code != 200 {
		t.Fatalf("overview with session: %d", rec.Code)
	}
	// State-changing calls need the X-NeoTunnel header (CSRF guard).
	body := `{"peer":"edge","mode":"6to4"}`
	if rec := do(h, "POST", "/secret/api/tunnels", body, c, nil); rec.Code != 403 {
		t.Errorf("POST without header: %d, want 403", rec.Code)
	}
	if rec := do(h, "POST", "/secret/api/tunnels", body, c, map[string]string{"X-NeoTunnel": "1"}); rec.Code != 200 {
		t.Errorf("POST with header: %d %s", rec.Code, rec.Body)
	}
	do(h, "POST", "/secret/api/logout", "", c, map[string]string{"X-NeoTunnel": "1"})
	if rec := do(h, "GET", "/secret/api/overview", "", c, nil); rec.Code != 401 {
		t.Errorf("session survived logout: %d", rec.Code)
	}
}

func TestLoginLockout(t *testing.T) {
	h := newPanel(t)
	for i := 0; i < 5; i++ {
		if rec := do(h, "POST", "/secret/api/login", `{"user":"admin","password":"nope"}`, nil, nil); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec := do(h, "POST", "/secret/api/login", `{"user":"admin","password":"right-password"}`, nil, nil); rec.Code != 429 {
		t.Errorf("correct password during lockout: %d, want 429", rec.Code)
	}
}

func TestCookieFlags(t *testing.T) {
	c := login(t, newPanel(t))[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/secret/" {
		t.Errorf("weak session cookie: %+v", c)
	}
}
