package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
	"neotunnel/internal/traffic"
	"neotunnel/internal/version"
)

type Client struct {
	base string
	http *http.Client
}

func newClient(id *identity.Identity, addr, pinned string, timeout time.Duration) (*Client, error) {
	tc, err := id.PinnedClient(pinned)
	if err != nil {
		return nil, err
	}
	return &Client{
		base: "https://" + addr,
		http: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tc, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
		},
	}, nil
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
	return newClient(id, net.JoinHostPort(p.Address, strconv.Itoa(port)), p.PublicKey, 30*time.Second)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any, hdr map[string]string) error {
	var body io.Reader
	if r, ok := in.(io.Reader); ok {
		body = r
	} else if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
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

// Register is called by a peer on its master to complete pairing.
func Register(ctx context.Context, id *identity.Identity, m *config.Managed) error {
	c, err := newClient(id, m.Master.Address, m.Master.PublicKey, 15*time.Second)
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/v1/register", RegisterRequest{
		Token: m.EnrollToken, Name: m.Self, Version: version.Version, Arch: version.Arch(),
		AgentPort: DefaultPort, Revision: m.Revision,
	}, nil, nil)
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
