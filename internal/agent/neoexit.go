package agent

// NeoExit on this node, run by the master's panel: requests passed to its
// local API, and installing it from a release the node downloads or the
// master sends.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"neotunnel/internal/neoexit"
)

func (s *Server) neoexitCall(w http.ResponseWriter, r *http.Request) {
	var req NeoExitCall
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	switch req.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		fail(w, http.StatusBadRequest, errors.New("bad method"))
		return
	}
	code, b, err := neoexit.Call(r.Context(), req.Method, req.Path, req.Body)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, NeoExitReply{Status: code, Body: b})
}

func (s *Server) neoexitFetch(w http.ResponseWriter, r *http.Request) {
	var req NeoExitFetch
	if err := readJSON(r, &req); err != nil || !neoexit.ValidVersion(req.Version) {
		fail(w, http.StatusBadRequest, errors.New("bad version"))
		return
	}
	arch := req.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	if arch != "amd64" && arch != "arm64" {
		fail(w, http.StatusBadRequest, errors.New("bad arch"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	v, err := neoexit.Fetch(ctx, req.Version, arch)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, NeoExitFetched{Version: v})
}

func (s *Server) neoexitGet(w http.ResponseWriter, r *http.Request) {
	p, err := neoexit.CachePath(r.PathValue("version"), r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fail(w, http.StatusNotFound, errors.New("not in the cache"))
		return
	}
	defer f.Close()
	sum, err := neoexit.FileSum(p)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Sha256", sum)
	io.Copy(w, f)
}

func (s *Server) neoexitPut(w http.ResponseWriter, r *http.Request) {
	err := neoexit.SaveCached(r.PathValue("version"), r.PathValue("name"), r.Body, r.Header.Get("X-Sha256"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// neoexitInstall installs a release: from the cache when the master sent
// it, else downloaded here.
func (s *Server) neoexitInstall(w http.ResponseWriter, r *http.Request) {
	var req NeoExitFetch
	if err := readJSON(r, &req); err != nil || !neoexit.ValidVersion(req.Version) {
		fail(w, http.StatusBadRequest, errors.New("bad version"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	v, err := neoexit.Fetch(ctx, req.Version, runtime.GOARCH)
	if err != nil {
		fail(w, http.StatusFailedDependency, fmt.Errorf("%s: %w", NeedFiles, err)) // the master sends the files instead
		return
	}
	res, err := neoexit.Install(ctx, v)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) neoexitUninstall(w http.ResponseWriter, r *http.Request) {
	if err := neoexit.Uninstall(); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
