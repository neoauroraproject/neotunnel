package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/manage"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// reply sends v, or the error as a 400.
func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) routes(m *http.ServeMux) {
	b := s.Backend
	m.HandleFunc("GET /api/overview", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ov, err := b.Overview()
		reply(w, ov, err)
	}))

	m.HandleFunc("POST /api/peers", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name, Address string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		setup, err := b.AddPeer(req.Name, req.Address)
		reply(w, setup, err)
	}))
	m.HandleFunc("DELETE /api/peers/{name}", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ch, err := b.RemovePeer(r.PathValue("name"))
		reply(w, ch, err)
	}))
	m.HandleFunc("GET /api/peers/{name}/setup", s.authed(func(w http.ResponseWriter, r *http.Request) {
		setup, err := b.PeerSetup(r.PathValue("name"))
		reply(w, setup, err)
	}))
	m.HandleFunc("POST /api/peers/{name}/sync", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, manage.Change{Peers: b.SyncPeers([]string{r.PathValue("name")})})
	}))
	m.HandleFunc("POST /api/sync", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, manage.Change{Peers: b.SyncPeers(nil)})
	}))
	m.HandleFunc("POST /api/peers/{name}/upgrade", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		v, err := b.UpgradePeer(ctx, r.PathValue("name"))
		reply(w, map[string]string{"version": v}, err)
	}))

	m.HandleFunc("POST /api/tunnels", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req manage.TunnelRequest
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.CreateTunnel(req)
		reply(w, ch, err)
	}))
	m.HandleFunc("DELETE /api/tunnels/{name}", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ch, err := b.RemoveTunnel(r.PathValue("name"))
		reply(w, ch, err)
	}))
	m.HandleFunc("PATCH /api/tunnels/{name}", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			manage.Settings
			Port uint16 `json:"port"`
		}
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.UpdateTunnel(r.PathValue("name"), req.Settings, req.Port)
		reply(w, ch, err)
	}))
	m.HandleFunc("POST /api/tunnels/{name}/restart", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		reply(w, map[string]bool{"ok": true}, b.RestartTunnel(ctx, r.PathValue("name")))
	}))
	m.HandleFunc("POST /api/tunnels/{name}/limits", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req manage.Limits
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.SetLimits(r.PathValue("name"), req)
		reply(w, ch, err)
	}))
	m.HandleFunc("POST /api/tunnels/{name}/mtu", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		rep, err := b.ProbeMTU(ctx, r.PathValue("name"))
		reply(w, rep, err)
	}))

	m.HandleFunc("GET /api/traffic", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Traffic())
	}))
	m.HandleFunc("POST /api/traffic/reset", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Tunnel string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		reply(w, map[string]bool{"ok": true}, b.ResetTraffic(req.Tunnel))
	}))

	m.HandleFunc("GET /api/system", s.authed(func(w http.ResponseWriter, r *http.Request) {
		si, err := b.System()
		reply(w, si, err)
	}))
	m.HandleFunc("POST /api/system/boost", s.authed(func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, b.Boost())
	}))
	m.HandleFunc("POST /api/system/restart", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		if s.Restart != nil {
			go func() {
				time.Sleep(time.Second)
				s.Restart()
			}()
		}
	}))
	m.HandleFunc("GET /api/backup", s.authed(func(w http.ResponseWriter, r *http.Request) {
		data, err := b.Backup()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="neotunnel-backup-`+time.Now().Format("2006-01-02")+`.json"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	}))

	m.HandleFunc("POST /api/forwards", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req manage.ForwardRequest
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.AddForward(req)
		reply(w, ch, err)
	}))
	m.HandleFunc("POST /api/forwards/update", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req manage.ForwardRequest
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.UpdateForward(req)
		reply(w, ch, err)
	}))
	m.HandleFunc("POST /api/forwards/remove", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Tunnel, Ports string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		ch, err := b.RemoveForward(req.Tunnel, req.Ports)
		reply(w, ch, err)
	}))

	m.HandleFunc("POST /api/check", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			manage.TunnelRequest
			All   bool `json:"all"`
			Speed bool `json:"speed"`
		}
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		if s.jobs.running() || !s.checking.TryLock() {
			fail(w, http.StatusConflict, "یک تست دیگر در حال اجراست؛ چند ثانیه بعد دوباره امتحان کنید")
			return
		}
		defer s.checking.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		res, err := b.Check(ctx, req.TunnelRequest, req.All, req.Speed)
		reply(w, res, err)
	}))
	m.HandleFunc("POST /api/jobs", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind, Target string
			Only         []string
		}
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		j, err := s.jobs.start(req.Kind, req.Target, func(ctx context.Context, log func(string), live func(string, float64), partial func(bench.Result)) ([]bench.Result, error) {
			if req.Kind == "test" {
				return b.TestTunnel(ctx, req.Target, log, live)
			}
			return b.Bench(ctx, req.Target, req.Only, log, live, partial)
		})
		if err != nil {
			fail(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, j.view())
	}))
	m.HandleFunc("GET /api/jobs", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.jobs.list())
	}))
	m.HandleFunc("GET /api/jobs/{id}", s.authed(func(w http.ResponseWriter, r *http.Request) {
		j, ok := s.jobs.get(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, "not found")
			return
		}
		writeJSON(w, http.StatusOK, j.view())
	}))
	m.HandleFunc("POST /api/jobs/{id}/cancel", s.authed(func(w http.ResponseWriter, r *http.Request) {
		s.jobs.cancel(r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	m.HandleFunc("POST /api/settings/node", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name, Address string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		reply(w, map[string]bool{"ok": true}, b.SetNode(req.Name, req.Address))
	}))
	m.HandleFunc("POST /api/settings/defaults", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req config.Defaults
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		reply(w, map[string]bool{"ok": true}, b.SetDefaults(req))
	}))
	m.HandleFunc("POST /api/settings/user", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ User string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		reply(w, map[string]bool{"ok": true}, b.SetUser(req.User))
	}))
	m.HandleFunc("POST /api/settings/password", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Current, Next string }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		reply(w, map[string]bool{"ok": true}, b.ChangePassword(req.Current, req.Next))
	}))
	m.HandleFunc("POST /api/settings/port", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Port int }
		if readJSON(r, &req) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		url, err := b.SetPanelPort(req.Port)
		reply(w, map[string]string{"url": url}, err)
		if err == nil && s.Restart != nil {
			go func() {
				time.Sleep(time.Second)
				s.Restart()
			}()
		}
	}))
}
