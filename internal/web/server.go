package web

import (
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

//go:embed static
var static embed.FS

const (
	sessionCookie = "nt_s"
	sessionTTL    = 12 * time.Hour
	maxFailures   = 5
	lockout       = 5 * time.Minute
)

// Server serves the panel under a secret path prefix.
type Server struct {
	Backend Backend
	Path    string // e.g. "/k3x9q2", no trailing slash
	// Restart is called after the panel port changed (the daemon exits and
	// systemd starts it again on the new port).
	Restart func()

	mu       sync.Mutex
	sessions map[string]time.Time
	failures map[string]*failure
	jobs     *jobs
}

type failure struct {
	count int
	until time.Time
}

func (s *Server) init() {
	s.sessions = map[string]time.Time{}
	s.failures = map[string]*failure{}
	s.jobs = newJobs()
}

// Handler returns the full HTTP handler (everything outside Path is a 404).
func (s *Server) Handler() http.Handler {
	s.init()
	inner := http.NewServeMux()
	assets, _ := fs.Sub(static, "static")
	// Revalidate assets so an upgrade's new app.js/app.css are picked up
	// instead of a stale cached copy.
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
	inner.Handle("GET /assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
	inner.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := static.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	inner.HandleFunc("POST /api/login", s.login)
	inner.HandleFunc("POST /api/logout", s.logout)
	s.routes(inner)

	outer := http.NewServeMux()
	outer.Handle(s.Path+"/", http.StripPrefix(s.Path, inner))
	outer.HandleFunc(s.Path, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.Path+"/", http.StatusFound)
	})
	return securityHeaders(outer)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; font-src 'self' https://cdn.jsdelivr.net data:; img-src 'self' data:; frame-ancestors 'none'")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the panel over HTTPS with a self-signed certificate.
func (s *Server) ListenAndServe(port int, address string) error {
	cert, err := certificate(address)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           s.Handler(),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	return srv.ListenAndServeTLS("", "")
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	s.mu.Lock()
	f := s.failures[ip]
	if f != nil && time.Now().Before(f.until) {
		s.mu.Unlock()
		fail(w, http.StatusTooManyRequests, "تعداد تلاش‌های ناموفق زیاد بود؛ چند دقیقه بعد دوباره امتحان کنید")
		return
	}
	s.mu.Unlock()

	var req struct{ User, Password string }
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "درخواست نامعتبر")
		return
	}
	if !s.Backend.Login(req.User, req.Password) {
		s.mu.Lock()
		if f == nil {
			f = &failure{}
			s.failures[ip] = f
		}
		f.count++
		if f.count >= maxFailures {
			f.count, f.until = 0, time.Now().Add(lockout)
		}
		s.mu.Unlock()
		fail(w, http.StatusUnauthorized, "نام کاربری یا رمز عبور اشتباه است")
		return
	}
	b := make([]byte, 32)
	rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	delete(s.failures, ip)
	now := time.Now()
	for k, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = now.Add(sessionTTL)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: s.Path + "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: s.Path + "/", MaxAge: -1, HttpOnly: true, Secure: true})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// authed wraps API handlers: valid session required; state-changing requests
// must also carry the X-NeoTunnel header, which cross-site pages can't send.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		ok := false
		if err == nil {
			s.mu.Lock()
			if exp, found := s.sessions[c.Value]; found && time.Now().Before(exp) {
				s.sessions[c.Value] = time.Now().Add(sessionTTL)
				ok = true
			}
			s.mu.Unlock()
		}
		if !ok {
			fail(w, http.StatusUnauthorized, "لطفاً وارد شوید")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-NeoTunnel") != "1" {
			fail(w, http.StatusForbidden, "درخواست نامعتبر")
			return
		}
		h(w, r)
	}
}
