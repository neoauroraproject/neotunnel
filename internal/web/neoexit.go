package web

// The NeoExit page: NeoExit on this server and on each peer, installed and
// run from here. Its own panel's API is passed through as it is, so this
// page can do everything that one does.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"neotunnel/internal/manage"
	"neotunnel/internal/neoexit"
)

func (s *Server) neoexitRoutes(m *http.ServeMux) {
	b := s.Backend
	m.HandleFunc("GET /api/neoexit", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.NeoExitServers())
	}))
	m.HandleFunc("POST /api/neoexit/{server}/install", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Version string }
		readJSON(r, &req)
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
		defer cancel()
		res, err := b.NeoExitInstall(ctx, r.PathValue("server"), req.Version)
		reply(w, res, err)
	}))
	m.HandleFunc("POST /api/neoexit/{server}/uninstall", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		reply(w, map[string]bool{"ok": true}, b.NeoExitUninstall(ctx, r.PathValue("server")))
	}))
	// NeoExit's panel API, its scripts, its login and a password reset.
	relay := func(prefix string) http.HandlerFunc {
		return s.authed(func(w http.ResponseWriter, r *http.Request) {
			rest := r.PathValue("rest")
			path := prefix + rest
			if prefix == "" {
				path = "/v1/panel/" + rest
			}
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			var body []byte
			if r.Method != http.MethodGet {
				body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			}
			code, out, err := b.NeoExitCall(r.Context(), r.PathValue("server"), r.Method, path, body)
			if err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			switch {
			case strings.HasPrefix(rest, "routing/files/"):
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", `attachment; filename="`+strings.TrimPrefix(rest, "routing/files/")+`"`)
			case strings.HasPrefix(path, "/v1/panel/assets/"):
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			default:
				w.Header().Set("Content-Type", "application/json")
			}
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(code)
			w.Write(out)
		})
	}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		m.HandleFunc(method+" /api/neoexit/{server}/api/{rest...}", relay("/v1/panel/api/"))
	}
	m.HandleFunc("GET /api/neoexit/{server}/assets/{rest}", relay("/v1/panel/assets/"))
	m.HandleFunc("GET /api/neoexit/{server}/login", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("rest", "login")
		relay("")(w, r)
	})
	m.HandleFunc("POST /api/neoexit/{server}/password", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("rest", "password")
		relay("")(w, r)
	})
}

func (l *Live) NeoExitServers() []manage.ExitServer {
	st, err := manage.Node()
	if err != nil {
		return nil
	}
	return manage.ExitServers(st)
}

func (l *Live) NeoExitCall(ctx context.Context, server, method, path string, body []byte) (int, []byte, error) {
	return manage.ExitCall(ctx, server, method, path, body)
}

func (l *Live) NeoExitInstall(ctx context.Context, server, version string) (neoexit.Installed, error) {
	return manage.ExitInstall(ctx, server, version)
}

func (l *Live) NeoExitUninstall(ctx context.Context, server string) error {
	return manage.ExitUninstall(ctx, server)
}
