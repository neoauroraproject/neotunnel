package agent

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
	"neotunnel/internal/probe"
)

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	config.Dir = t.TempDir()
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// serve starts an agent for id on a random port, using the current config.Dir.
func serve(t *testing.T, id *identity.Identity) string {
	t.Helper()
	s := &Server{ID: id, Probe: probe.NewServer(), Started: time.Now()}
	ts := httptest.NewUnstartedServer(s.Handler())
	tc, err := id.Server()
	if err != nil {
		t.Fatal(err)
	}
	ts.TLS = tc
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "https://")
}

func TestRegisterPairsPeer(t *testing.T) {
	peerID := newIdentity(t)
	masterID := newIdentity(t) // config.Dir now points at the master
	const token = "secret-token"
	config.Save(&config.State{Self: "hub", Peers: []config.Peer{{
		Name: "edge", Address: "203.0.113.10",
		EnrollToken: token, EnrollExpires: time.Now().Add(time.Hour),
	}}})
	addr := serve(t, masterID)
	managed := func(tok string) *config.Managed {
		return &config.Managed{
			Master: config.MasterRef{Name: "hub", PublicKey: masterID.PublicString(), Address: addr},
			Self:   "edge", Revision: 4, EnrollToken: tok,
		}
	}
	ctx := context.Background()

	// The binary download is token-gated and works without a client cert (curl).
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	for tok, want := range map[string]int{token: 200, "wrong": 403} {
		resp, err := insecure.Get("https://" + addr + "/dl/" + tok)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("download with token %q: status %d, want %d", tok, resp.StatusCode, want)
		}
	}

	if err := Register(ctx, peerID, managed("wrong")); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := Register(ctx, peerID, managed(token)); err != nil {
		t.Fatal(err)
	}
	st, _ := config.Load()
	p, _ := st.Peer("edge")
	if p.PublicKey != peerID.PublicString() || p.EnrollToken != "" || p.AppliedRevision != 4 {
		t.Fatalf("peer not paired: %+v", p)
	}
	if err := Register(ctx, peerID, managed(token)); err == nil {
		t.Error("token reused after pairing")
	}

	// A peer must refuse a master whose key does not match its setup key.
	impostor := managed(token)
	impostor.Master.PublicKey = peerID.PublicString()
	if err := Register(ctx, peerID, impostor); err == nil {
		t.Error("connected to a server with the wrong key")
	}
}

func TestOnlyMasterControlsPeer(t *testing.T) {
	masterID := newIdentity(t)
	strangerID := newIdentity(t)
	peerID := newIdentity(t) // config.Dir now points at the peer
	config.Save(&config.State{Self: "edge", Managed: &config.Managed{
		Master: config.MasterRef{Name: "hub", PublicKey: masterID.PublicString()}, Self: "edge", Revision: 7,
	}})
	addr := serve(t, peerID)
	host, port, _ := net.SplitHostPort(addr)
	p := config.Peer{Name: "edge", Address: host, PublicKey: peerID.PublicString()}
	p.AgentPort, _ = strconv.Atoi(port)
	ctx := context.Background()

	c, err := ToPeer(masterID, p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "edge" || s.Revision != 7 {
		t.Errorf("status %+v", s)
	}

	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if resp, err := insecure.Get("https://" + addr + "/v1/status"); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status without a client cert: %d, want 401", resp.StatusCode)
		}
	}

	c2, _ := ToPeer(strangerID, p)
	if _, err := c2.Status(ctx); err == nil {
		t.Error("a non-master key was allowed to read status")
	}
	if err := c2.OpenProbe(ctx, probe.NewToken().String(), time.Minute); err == nil {
		t.Error("a non-master key opened a probe session")
	}
	if _, err := c2.Apply(ctx, ApplyRequest{Revision: 99}); err == nil {
		t.Error("a non-master key pushed configuration")
	}

	// Stale pushes are refused.
	if _, err := c.Apply(ctx, ApplyRequest{Revision: 3}); err == nil {
		t.Error("stale revision accepted")
	}
}
