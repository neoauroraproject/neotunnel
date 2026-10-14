package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neotunnel/internal/web"
	"neotunnel/internal/web/demo"
)

func newPanel(t *testing.T) http.Handler {
	t.Helper()
	return (&web.Server{Backend: demo.New(), Path: "/secret"}).Handler()
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
	rec := do(h, "POST", "/secret/api/login", `{"user":"admin","password":"demo"}`, nil, nil)
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
	body := `{"peer":"germany","mode":"6to4"}`
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
	if rec := do(h, "POST", "/secret/api/login", `{"user":"admin","password":"demo"}`, nil, nil); rec.Code != 429 {
		t.Errorf("correct password during lockout: %d, want 429", rec.Code)
	}
}

func TestCookieFlags(t *testing.T) {
	c := login(t, newPanel(t))[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/secret/" {
		t.Errorf("weak session cookie: %+v", c)
	}
}
