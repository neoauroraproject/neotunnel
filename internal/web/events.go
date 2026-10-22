package web

import (
	"net/http"
	"strconv"

	"neotunnel/internal/events"
)

func (s *Server) eventRoutes(m *http.ServeMux) {
	// A tunnel's history, newest first.
	m.HandleFunc("GET /api/tunnels/{name}/events", s.authed(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		out := events.Of(r.PathValue("name"), limit)
		if out == nil {
			out = []events.Event{}
		}
		writeJSON(w, http.StatusOK, out)
	}))
}
