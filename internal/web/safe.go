package web

import (
	"net/http"

	"neotunnel/internal/manage"
)

// changing is authed for the routes that change tunnels, forwards or
// peers: they wait while a safe change is pending, since undoing it would
// undo them too.
func (s *Server) changing(h http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := s.Backend.SafeStatus(); p != nil {
			fail(w, http.StatusConflict, "یک تغییر امن منتظر تأیید است؛ اول آن را نگه دارید یا برگردانید")
			return
		}
		h(w, r)
	})
}

// change runs fn and replies with its outcome; with ?safe=1 it runs as a
// safe change, reverted on both servers unless kept within a minute.
func (s *Server) change(w http.ResponseWriter, r *http.Request, what string, fn func() (manage.Change, error)) {
	if r.URL.Query().Get("safe") != "1" {
		ch, err := fn()
		reply(w, ch, err)
		return
	}
	ch, p, err := s.Backend.Safely(what, fn)
	reply(w, struct {
		manage.Change
		Safe *SafeView `json:"safe,omitempty"`
	}{ch, p}, err)
}

func (s *Server) safeRoutes(m *http.ServeMux) {
	b := s.Backend
	m.HandleFunc("GET /api/safe", s.authed(func(w http.ResponseWriter, r *http.Request) {
		p, last := b.SafeStatus()
		writeJSON(w, http.StatusOK, map[string]any{"pending": p, "last": last})
	}))
	m.HandleFunc("POST /api/safe/{id}/keep", s.authed(func(w http.ResponseWriter, r *http.Request) {
		o, err := b.KeepSafe(r.PathValue("id"))
		p, _ := b.SafeStatus()
		reply(w, map[string]any{"pending": p, "outcome": o}, err)
	}))
	m.HandleFunc("POST /api/safe/{id}/undo", s.authed(func(w http.ResponseWriter, r *http.Request) {
		o, err := b.UndoSafe(r.PathValue("id"))
		reply(w, map[string]any{"outcome": o}, err)
	}))
}
