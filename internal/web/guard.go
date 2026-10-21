package web

import (
	"context"
	"net/http"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/guard"
	"neotunnel/internal/manage"
)

// GuardView is the firewall page: its settings and what it does now.
type GuardView struct {
	Settings config.Guard `json:"settings"`
	Status   guard.Status `json:"status"`
}

func guardView() (GuardView, error) {
	st, err := config.Load()
	if err != nil {
		return GuardView{}, err
	}
	v := GuardView{}
	if st.Guard != nil {
		v.Settings = *st.Guard
	}
	if v.Settings.ICMP == "" {
		v.Settings.ICMP = "closed"
	}
	_, v.Status = guard.Build(st)
	return v, nil
}

func (s *Server) guardRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/guard", s.authed(func(w http.ResponseWriter, r *http.Request) {
		v, err := guardView()
		reply(w, v, err)
	}))
	m.HandleFunc("POST /api/guard", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var g config.Guard
		if readJSON(r, &g) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		if _, err := manage.SetGuard(g, true); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		// At once, not at the daemon's next look.
		if st, err := config.Load(); err == nil {
			if err := engine.ApplyGuard(st); err != nil {
				v, _ := guardView()
				v.Status.Error = err.Error()
				writeJSON(w, http.StatusOK, v)
				return
			}
		}
		v, err := guardView()
		reply(w, v, err)
	}))
	// The panel calls this right after turning the firewall on: a request
	// that arrives proves the panel still reaches the server.
	m.HandleFunc("POST /api/guard/confirm", s.authed(func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, manage.ConfirmGuard())
	}))
	m.HandleFunc("POST /api/guard/geo", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		info, err := guard.UpdateIR(ctx)
		if err != nil {
			fail(w, http.StatusBadGateway, "دانلود فهرست IP ایران ممکن نشد؛ نسخه‌ی همراه برنامه استفاده می‌شود")
			return
		}
		reply(w, info, nil)
	}))
}
