package agent

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/probe"
	"neotunnel/internal/traffic"
	"neotunnel/internal/version"
)

type Server struct {
	ID      *identity.Identity
	Probe   *probe.Server
	Started time.Time
	// Applied is called after a master pushed new state (the daemon re-runs
	// its reconcile loop).
	Applied func()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Called by peers on their master.
	mux.HandleFunc("POST /v1/register", s.register)
	mux.HandleFunc("GET /dl/{token}", s.download)
	// Called by our master.
	mux.HandleFunc("GET /v1/status", s.fromMaster(s.status))
	mux.HandleFunc("POST /v1/apply", s.fromMaster(s.apply))
	mux.HandleFunc("POST /v1/probe", s.fromMaster(s.openProbe))
	mux.HandleFunc("POST /v1/bench/up", s.fromMaster(s.benchUp))
	mux.HandleFunc("POST /v1/bench/down", s.fromMaster(s.benchDown))
	mux.HandleFunc("POST /v1/upgrade", s.fromMaster(s.upgrade))
	mux.HandleFunc("POST /v1/traffic/exclude", s.fromMaster(s.excludeTraffic))
	return mux
}

// ListenAndServe serves the agent API over TLS on addr.
func (s *Server) ListenAndServe(addr string) error {
	tc, err := s.ID.Server()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		TLSConfig:         tc,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // scanners hitting the port are noise
	}
	return srv.ListenAndServeTLS("", "")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}

// fromMaster allows a request only if the client holds our master's key.
func (s *Server) fromMaster(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, err := identity.PeerKey(r.TLS.PeerCertificates)
		if err != nil {
			fail(w, http.StatusUnauthorized, err)
			return
		}
		st, err := config.Load()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		if st.Managed == nil || subtle.ConstantTimeCompare([]byte(st.Managed.Master.PublicKey), []byte(key)) != 1 {
			fail(w, http.StatusForbidden, errors.New("not my master"))
			return
		}
		h(w, r)
	}
}

// validEnroll reports whether tok is p's current enrollment token.
func validEnroll(p config.Peer, tok string) bool {
	return p.EnrollToken != "" && tok != "" &&
		subtle.ConstantTimeCompare([]byte(p.EnrollToken), []byte(tok)) == 1 &&
		time.Now().Before(p.EnrollExpires)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	key, err := identity.PeerKey(r.TLS.PeerCertificates)
	if err != nil {
		fail(w, http.StatusUnauthorized, err)
		return
	}
	var req RegisterRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	_, err = config.Update(func(st *config.State) error {
		p, ok := st.Peer(req.Name)
		if !ok {
			return errors.New("unknown peer")
		}
		if !validEnroll(*p, req.Token) {
			return errors.New("invalid or expired enrollment token; show a new connect command on the main server: neotunnel peer connect " + req.Name)
		}
		p.PublicKey = key
		p.EnrollToken, p.EnrollExpires = "", time.Time{}
		p.AppliedRevision = req.Revision
		if p.Address == "" {
			p.Address = remote
		}
		if req.AgentPort != 0 {
			p.AgentPort = req.AgentPort
		}
		p.Version, p.Arch, p.LastSeen = req.Version, req.Arch, time.Now()
		return nil
	})
	if err != nil {
		fail(w, http.StatusForbidden, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "registered"})
}

// download serves this binary to a peer holding a valid enrollment token, so
// the setup one-liner works without any external download site.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	st, err := config.Load()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	tok := r.PathValue("token")
	ok := false
	for _, p := range st.Peers {
		ok = ok || validEnroll(p, tok)
	}
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, exe)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st, err := config.Load()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, LocalStatus(st, s.Started))
}

// LocalStatus reports this node's tunnels and counters.
func LocalStatus(st *config.State, started time.Time) Status {
	out := Status{Name: st.Self, Version: version.Version, Arch: version.Arch(), Address6: engine.PublicIPv6()}
	if st.Managed != nil {
		out.Revision = st.Managed.Revision
	}
	if !started.IsZero() {
		out.Uptime = int64(time.Since(started).Seconds())
	}
	ts, _ := st.Effective()
	for _, t := range ts {
		ls := engine.State(engine.Ifname(t.Name))
		out.Tunnels = append(out.Tunnels, TunnelStatus{
			Name: t.Name, Iface: engine.Ifname(t.Name), Kind: string(t.Kind),
			Up: ls.Up, MTU: ls.MTU, RxBytes: ls.RxBytes, TxBytes: ls.TxBytes,
		})
	}
	return out
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	st, err := config.Update(func(st *config.State) error {
		m := st.Managed
		if req.Revision < m.Revision {
			return fmt.Errorf("stale revision %d < %d", req.Revision, m.Revision)
		}
		m.Revision, m.Tunnels, m.Forwards = req.Revision, req.Tunnels, req.Forwards
		return nil
	})
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	res := engine.Apply(st)
	resp := ApplyResponse{Revision: req.Revision}
	for _, e := range res.Errors {
		resp.Errors = append(resp.Errors, e.Error())
	}
	if s.Applied != nil {
		s.Applied()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) openProbe(w http.ResponseWriter, r *http.Request) {
	var req ProbeRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	tok, ok := probe.ParseToken(req.Token)
	if !ok || req.Seconds <= 0 || req.Seconds > 900 {
		fail(w, http.StatusBadRequest, errors.New("bad probe request"))
		return
	}
	s.Probe.Open(tok, time.Duration(req.Seconds)*time.Second)
	writeJSON(w, http.StatusOK, map[string]int{"port": probe.DefaultPort})
}

func (s *Server) benchUp(w http.ResponseWriter, r *http.Request) {
	var req BenchUpRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.TTL <= 0 || req.TTL > 1800 {
		req.TTL = 600
	}
	isSelf := func(n string) bool { return n == req.Self }
	specs, errs := engine.RenderTunnels(isSelf, req.Tunnels, engine.RouteSource)
	if len(errs) > 0 {
		fail(w, http.StatusBadRequest, errors.Join(errs...))
		return
	}
	for i := range specs {
		specs[i].Name = BenchIfname(specs[i].Tunnel)
		if specs[i].Parent != "" {
			specs[i].Parent = BenchIfname(strings.TrimPrefix(specs[i].Parent, engine.IfPrefix))
		}
	}
	if err := engine.ApplyBench(specs, time.Duration(req.TTL)*time.Second); err != nil {
		names := make([]string, len(specs))
		for i, s := range specs {
			names[i] = s.Name
		}
		engine.RemoveBench(names) // what came up before the failure
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, BenchUpResponse{Specs: specs})
}

// BenchIfname keeps temporary test interfaces apart from real tunnels.
func BenchIfname(tunnel string) string { return "ntx-" + tunnel }

func (s *Server) benchDown(w http.ResponseWriter, r *http.Request) {
	var req BenchDownRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	engine.RemoveBench(req.Names)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "removed"})
}

// upgrade replaces this binary with the one in the body, after checking its
// hash and that it runs, then exits so systemd restarts the new version.
func (s *Server) upgrade(w http.ResponseWriter, r *http.Request) {
	want := r.Header.Get("X-Sha256")
	exe, err := os.Executable()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".neotunnel-new-*")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r.Body, 256<<20))
	tmp.Close()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		fail(w, http.StatusBadRequest, fmt.Errorf("checksum mismatch"))
		return
	}
	os.Chmod(tmp.Name(), 0o755)
	out, err := exec.Command(tmp.Name(), "version").Output()
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("new binary does not run here (wrong architecture?): %v", err))
		return
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": strings.TrimSpace(string(out))})
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0) // systemd (Restart=always) starts the new binary
	}()
}

func (s *Server) excludeTraffic(w http.ResponseWriter, r *http.Request) {
	var req ExcludeRequest
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := traffic.RequestExclude(req.Tunnel, req.Bytes); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
