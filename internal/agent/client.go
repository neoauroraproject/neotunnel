package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/traffic"
	"neotunnel/internal/version"
)

type Client struct {
	// bases are tried in order (inner tunnel addresses first, then the
	// public one); see innerBases.
	bases  []string
	pinned string // the far end's key: which route worked is kept by it
	// veiled reaches the far end with its TLS hidden (see veil.go), plain as
	// it is, which is what nodes before v0.8.15 understand.
	veiled, plain *http.Client
}

// route is one way to reach the far end.
type route struct {
	base  string
	plain bool
}

// lastGood remembers, per far end (by its key), the route that last answered,
// so the next call starts there instead of waiting out a dead one first.
var lastGood sync.Map

// routes lists every base veiled, then every base plain, with the route that
// last answered first. Plain comes last for older nodes: they refuse a veil at
// once, while on a filtered path it is plain TLS that stalls.
func (c *Client) routes() []route {
	out := make([]route, 0, 2*len(c.bases))
	for _, plain := range []bool{false, true} {
		for _, b := range c.bases {
			out = append(out, route{b, plain})
		}
	}
	if v, ok := lastGood.Load(c.pinned); ok {
		// That route may be gone (a tunnel removed): then the order stays.
		if at := slices.Index(out, v.(route)); at > 0 {
			out = slices.Insert(slices.Delete(out, at, at+1), 0, v.(route))
		}
	}
	return out
}

func newClient(id *identity.Identity, bases []string, pinned string, timeout time.Duration) (*Client, error) {
	tc, err := id.PinnedClient(pinned)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client := func(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
		return &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tc,
				DialContext:     dial,
				// Every call gets its own connection. A client lasts one call,
				// so one left open would stay open for good, and a new
				// connection is also what gets past a path that stalled the
				// last one.
				DisableKeepAlives: true,
				// Bound each phase on its own, so a stalled link reports which
				// phase failed instead of one opaque deadline: the TCP connect
				// succeeds, the TLS session to the agent port hangs, or the
				// request is sent but no response comes back. On a filtered
				// path to a bare IP these look very different, and the panel
				// shows the phase.
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
		}
	}
	return &Client{
		bases:  bases,
		pinned: pinned,
		plain:  client(dialer.DialContext),
		veiled: client(func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			v, err := veilClient(conn, pinned)
			if err != nil {
				conn.Close()
				return nil, err
			}
			return v, nil
		}),
	}, nil
}

// innerBases returns the agent's https base for each up tunnel that reaches
// remote, through the tunnel's own inner address. On a path where the public
// link lets TCP through but a middlebox stalls the TLS session to the bare
// IP (so the agent times out while a data tunnel still carries traffic),
// this reaches the agent over the encrypted tunnel instead. The pinned key
// and TLS are unchanged; only the route differs. Tried before the public
// address, which stays as the fallback and the only path before any tunnel
// is up.
func innerBases(remote string, port int) []string {
	st, err := config.Load()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	ts, _ := st.Effective()
	for _, t := range ts {
		side, ok := t.SideOf(remote)
		if !ok || !engine.TunnelState(t.Name).Up {
			continue
		}
		for _, netStr := range []string{t.Net4, t.Net6} {
			if netStr == "" {
				continue
			}
			if pfx, ok := config.Inner(netStr, side); ok {
				u := "https://" + net.JoinHostPort(pfx.Addr().String(), strconv.Itoa(port))
				if !seen[u] {
					seen[u] = true
					out = append(out, u)
				}
			}
		}
	}
	return out
}

// ToPeer connects to a paired peer as its master.
func ToPeer(id *identity.Identity, p config.Peer) (*Client, error) {
	if !p.Paired() {
		return nil, fmt.Errorf("peer %s is not paired yet: run its connect command on it", p.Name)
	}
	port := p.AgentPort
	if port == 0 {
		port = DefaultPort
	}
	bases := append(innerBases(p.Name, port), "https://"+net.JoinHostPort(p.Address, strconv.Itoa(port)))
	if port != DefaultPort {
		// A peer paired on 7710 before it had a port of its own keeps
		// listening there; the last resort until its new port answers.
		bases = append(bases, "https://"+net.JoinHostPort(p.Address, strconv.Itoa(DefaultPort)))
	}
	return newClient(id, bases, p.PublicKey, 30*time.Second)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any, hdr map[string]string) error {
	// Read the body into memory once, so it can be replayed to the next base
	// if the first never answered (the body is small JSON, or the upgrade
	// binary we already hold).
	var body []byte
	if r, ok := in.(io.Reader); ok {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		body = b
	} else if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = b
	}

	var lastErr error
	unreachable := map[string]bool{}
	for _, rt := range c.routes() {
		// A base nothing connects to won't do better without the veil.
		if unreachable[rt.base] {
			continue
		}
		if lastErr != nil && ctx.Err() != nil {
			return lastErr
		}
		var r io.Reader
		if in != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, rt.base+path, r)
		if err != nil {
			return err
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		hc := c.veiled
		if rt.plain {
			hc = c.plain
		}
		resp, err := hc.Do(req)
		if err != nil {
			// No answer on this route: try the next one (e.g. the public
			// address when the inner tunnel address is not reachable, or the
			// reverse). The request was not processed, so replaying is safe.
			// It isn't the route to start with next time either.
			lastErr = err
			lastGood.CompareAndDelete(c.pinned, rt)
			var op *net.OpError
			if errors.As(err, &op) && op.Op == "dial" {
				unreachable[rt.base] = true
			}
			continue
		}
		lastGood.Store(c.pinned, rt)
		return c.handle(method, path, resp, out)
	}
	return lastErr
}

func (c *Client) handle(method, path string, resp *http.Response, out any) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%s %s: %w", method, path, ErrUnsupported)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	return s, c.do(ctx, "GET", "/v1/status", nil, &s, nil)
}

func (c *Client) Apply(ctx context.Context, req ApplyRequest) (ApplyResponse, error) {
	var r ApplyResponse
	return r, c.do(ctx, "POST", "/v1/apply", req, &r, nil)
}

func (c *Client) OpenProbe(ctx context.Context, token string, d time.Duration) error {
	return c.do(ctx, "POST", "/v1/probe", ProbeRequest{Token: token, Seconds: int(d.Seconds())}, nil, nil)
}

func (c *Client) BenchUp(ctx context.Context, req BenchUpRequest) (BenchUpResponse, error) {
	var r BenchUpResponse
	return r, c.do(ctx, "POST", "/v1/bench/up", req, &r, nil)
}

func (c *Client) BenchDown(ctx context.Context, names []string) error {
	return c.do(ctx, "POST", "/v1/bench/down", BenchDownRequest{Names: names}, nil, nil)
}

// Upgrade sends this node's own binary to the peer.
func (c *Client) Upgrade(ctx context.Context) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	var r struct{ Version string }
	err = c.do(ctx, "POST", "/v1/upgrade", bytes.NewReader(b), &r, map[string]string{"X-Sha256": hex.EncodeToString(sum[:])})
	return r.Version, err
}

// Register is called by a peer on its master to complete pairing. It reaches
// the master over a tunnel's inner address when one is up, so pairing can
// complete even where the master's public TLS path is filtered, falling back
// to the master's public address (the only route before a tunnel exists).
func Register(ctx context.Context, id *identity.Identity, m *config.Managed) error {
	port := DefaultPort
	if _, p, err := net.SplitHostPort(m.Master.Address); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	bases := append(innerBases(m.Master.Name, port), "https://"+m.Master.Address)
	c, err := newClient(id, bases, m.Master.PublicKey, 15*time.Second)
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/v1/register", RegisterRequest{
		Token: m.EnrollToken, Name: m.Self, Version: version.Version, Arch: version.Arch(),
		AgentPort: listenPort(), Revision: m.Revision,
	}, nil, nil)
}

// listenPort is this node's own agent port, which its master dials.
func listenPort() int {
	if st, err := config.Load(); err == nil {
		return st.ListenAgentPort()
	}
	return DefaultPort
}

// ExcludeTraffic tells the peer to keep a speed test out of a tunnel's
// totals. Peers older than this call answer 404, which is fine to ignore.
func (c *Client) ExcludeTraffic(ctx context.Context, tunnel string, d traffic.Pair) error {
	return c.do(ctx, "POST", "/v1/traffic/exclude", ExcludeRequest{Tunnel: tunnel, Bytes: d}, nil, nil)
}

// Restart asks the peer to take tunnels down and up again. Peers older than
// this call answer 404.
func (c *Client) Restart(ctx context.Context, tunnels []string) error {
	return c.do(ctx, "POST", "/v1/restart", RestartRequest{Tunnels: tunnels}, nil, nil)
}

// ErrUnsupported is returned for calls the peer's version doesn't have.
var ErrUnsupported = errors.New("not supported by this server's version")

// SafeConfirm keeps a safe change on the peer. Peers older than safe
// changes answer ErrUnsupported.
func (c *Client) SafeConfirm(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/v1/safe/confirm", SafeConfirmRequest{ID: id}, nil, nil)
}
