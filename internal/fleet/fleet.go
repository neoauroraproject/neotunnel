// Package fleet is the master's view of its peers: pushing configuration,
// building setup keys for peers that can't be reached, and upgrades.
package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/setuplink"
	"neotunnel/internal/version"
)

const EnrollTTL = 24 * time.Hour

// Push sends the peer its current configuration. On success the peer's
// applied revision is recorded.
func Push(ctx context.Context, id *identity.Identity, st *config.State, peer string) (agent.ApplyResponse, error) {
	p, ok := st.Peer(peer)
	if !ok {
		return agent.ApplyResponse{}, fmt.Errorf("unknown peer %q", peer)
	}
	c, err := agent.ToPeer(id, *p)
	if err != nil {
		return agent.ApplyResponse{}, err
	}
	ts, fs := st.ForPeer(peer)
	resp, err := c.Apply(ctx, agent.ApplyRequest{Revision: st.Revision, Tunnels: ts, Forwards: fs})
	if err != nil {
		return resp, err
	}
	config.Update(func(s *config.State) error {
		if p, ok := s.Peer(peer); ok {
			p.AppliedRevision, p.LastSeen = resp.Revision, time.Now()
		}
		return nil
	})
	return resp, nil
}

// PushAll pushes to every peer, returning per-peer errors.
func PushAll(ctx context.Context, id *identity.Identity, st *config.State, peers []string) map[string]error {
	out := map[string]error{}
	for _, p := range peers {
		_, err := Push(ctx, id, st, p)
		out[p] = err
	}
	return out
}

// Setup is everything a peer needs to join or catch up.
type Setup struct {
	Peer    string `json:"peer"`
	Address string `json:"address"` // the peer's IP, for display
	Paired  bool   `json:"paired"`
	Key     string `json:"key"`   // signed setup key (NT1....)
	Apply   string `json:"apply"` // sudo neotunnel apply '<key>'
	// Install downloads the binary from this master, verifies it and applies
	// (only while the peer isn't paired).
	Install string `json:"install,omitempty"`
	// GitHub installs from the public release instead, then applies.
	GitHub string `json:"github,omitempty"`
}

func randToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// MakeSetup builds a signed setup key for peer carrying its full configuration.
// Unpaired peers also get a fresh enrollment token (valid EnrollTTL) and an
// install one-liner that downloads this binary from the master and checks it.
func MakeSetup(id *identity.Identity, peer string) (Setup, error) {
	var token string
	st, err := config.Update(func(s *config.State) error {
		p, ok := s.Peer(peer)
		if !ok {
			return fmt.Errorf("unknown peer %q", peer)
		}
		if !p.Paired() {
			if p.EnrollToken == "" || time.Now().After(p.EnrollExpires) {
				p.EnrollToken = randToken()
			}
			p.EnrollExpires = time.Now().Add(EnrollTTL)
			token = p.EnrollToken
		}
		return nil
	})
	if err != nil {
		return Setup{}, err
	}
	if st.Address == "" {
		return Setup{}, fmt.Errorf("this node's public address is unknown; set it with: neotunnel node --address <ip>")
	}
	ts, fs := st.ForPeer(peer)
	key, err := setuplink.Encode(id, setuplink.Payload{
		Issued:      time.Now().UTC(),
		Master:      config.MasterRef{Name: st.Self, Address: net.JoinHostPort(st.Address, strconv.Itoa(agent.DefaultPort))},
		Peer:        peer,
		EnrollToken: token,
		Revision:    st.Revision,
		Tunnels:     ts,
		Forwards:    fs,
	})
	if err != nil {
		return Setup{}, err
	}
	p, _ := st.Peer(peer)
	s := Setup{
		Peer: peer, Address: p.Address, Paired: p.Paired(), Key: key,
		Apply: fmt.Sprintf("sudo neotunnel apply '%s'", key),
	}
	if token != "" {
		s.GitHub = fmt.Sprintf("curl -fsSL %s | sudo bash -s -- --no-panel --no-menu && %s", version.InstallURL, s.Apply)
		exe, err := os.Executable()
		if err == nil {
			if b, err := os.ReadFile(exe); err == nil {
				sum := sha256.Sum256(b)
				url := fmt.Sprintf("https://%s/dl/%s", net.JoinHostPort(st.Address, strconv.Itoa(agent.DefaultPort)), token)
				// -k is safe here: the checksum is verified before anything runs.
				s.Install = fmt.Sprintf("curl -fsSk %s -o /tmp/neotunnel && echo '%x  /tmp/neotunnel' | sha256sum -c --quiet && sudo install -m 755 /tmp/neotunnel /usr/local/bin/neotunnel && rm /tmp/neotunnel && sudo neotunnel apply '%s'",
					url, sum, key)
			}
		}
	}
	return s, nil
}

// DetectAddress guesses this node's public address as the source the kernel
// would use to reach target (or a well-known address).
func DetectAddress(target string) string {
	a, err := netip.ParseAddr(target)
	if err != nil {
		a = netip.MustParseAddr("1.1.1.1")
	}
	src, err := engine.RouteSource(a)
	if err != nil {
		return ""
	}
	return src.String()
}

// Upgrade pushes this binary to a peer.
func Upgrade(ctx context.Context, id *identity.Identity, st *config.State, peer string) (string, error) {
	p, ok := st.Peer(peer)
	if !ok {
		return "", fmt.Errorf("unknown peer %q", peer)
	}
	c, err := agent.ToPeer(id, *p)
	if err != nil {
		return "", err
	}
	return c.Upgrade(ctx)
}
