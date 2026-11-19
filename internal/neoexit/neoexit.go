// Package neoexit finds NeoExit (free exits for AI services, a separate
// program) on this server, installs it, and passes the panel's requests to
// it. NeoExit answers on a unix socket only root can open, with the same
// API its own panel uses (/v1/panel/api/…), so NeoTunnel's panel can do
// everything NeoExit's own can, on this server and on its peers.
package neoexit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SocketPath is where NeoExit's local API listens.
const SocketPath = "/run/neoexit/api.sock"

// Status is NeoExit's /v1/status. Fields are only ever added there, so
// this may be a subset.
type Status struct {
	Version  string    `json:"version"`
	Panel    string    `json:"panel"` // ":port/path/"; the server's address goes in front
	Proxy    string    `json:"proxy"`
	Services []Service `json:"services"`
	Exits    []Exit    `json:"exits"`
	LastRun  time.Time `json:"last_run"`
	// API is the version of /v1/panel (0: too old to manage from here).
	API int `json:"api,omitempty"`
}

type Service struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // ok, geo, challenge, error, down
	Exit  string `json:"exit"`
}

type Exit struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Up      bool   `json:"up"`
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	Ms      int    `json:"ms,omitempty"`
}

var client = &http.Client{
	Timeout: 2 * time.Minute, // an update check or a test run may take a while
	Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", SocketPath)
	}},
}

var (
	mu       sync.Mutex
	cached   *Status
	cachedAt time.Time
)

// Local is NeoExit on this server, or nil when it isn't installed or
// isn't running. Answers are kept for 10 seconds.
func Local() *Status {
	mu.Lock()
	defer mu.Unlock()
	if time.Since(cachedAt) < 10*time.Second {
		return cached
	}
	cachedAt = time.Now()
	cached = nil
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, b, err := Call(ctx, "GET", "/v1/status", nil)
	if err != nil || code != http.StatusOK {
		return nil
	}
	var s Status
	if json.Unmarshal(b, &s) == nil {
		cached = &s
	}
	return cached
}

// Forget drops the cached status (after an install or a change).
func Forget() {
	mu.Lock()
	cachedAt = time.Time{}
	mu.Unlock()
}

// Call sends a request to NeoExit's local API and returns its answer.
// Only /v1/ paths are passed on.
func Call(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if !strings.HasPrefix(path, "/v1/") || strings.Contains(path, "..") {
		return 0, nil, fmt.Errorf("bad NeoExit path %q", path)
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://neoexit"+path, r)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("NeoExit isn't running here: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if method != http.MethodGet {
		Forget()
	}
	return resp.StatusCode, b, err
}

// What peers reported in their last status, kept in memory only (it
// changes every few minutes; the settings file shouldn't).
var (
	pmu   sync.Mutex
	peers = map[string]PeerStatus{}
)

// PeerStatus is NeoExit on a peer, as it last said.
type PeerStatus struct {
	Status *Status
	State  string // StateNone etc.; "" from a peer that can't manage NeoExit
	at     time.Time
}

func SetPeer(name string, s *Status, state string) {
	pmu.Lock()
	defer pmu.Unlock()
	peers[name] = PeerStatus{s, state, time.Now()}
}

// Peer is what a peer last said about its NeoExit (zero: nothing, or the
// peer hasn't answered for 10 minutes).
func Peer(name string) PeerStatus {
	pmu.Lock()
	defer pmu.Unlock()
	p, ok := peers[name]
	if !ok || time.Since(p.at) > 10*time.Minute {
		return PeerStatus{}
	}
	return p
}
