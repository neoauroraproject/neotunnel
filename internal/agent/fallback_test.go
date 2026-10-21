package agent

import (
	"context"
	"testing"
	"time"

	"neotunnel/internal/config"
)

// When the first base does not answer (as on a path where the public TLS
// session stalls while a data tunnel still carries traffic), the client
// falls through to the next base, so one unreachable route does not fail the
// call. innerBases puts the tunnel address first and the public one last.
func TestFallsBackToWorkingBase(t *testing.T) {
	masterID := newIdentity(t)
	peerID := newIdentity(t) // config.Dir now points at the peer's dir
	config.Save(&config.State{Self: "edge", Managed: &config.Managed{
		Master: config.MasterRef{Name: "hub", PublicKey: masterID.PublicString()}, Self: "edge", Revision: 7,
	}})
	live := serve(t, peerID)

	// Nothing listens on this base, so the first attempt is refused and the client
	// must use the second.
	dead := "https://127.0.0.1:1" // nothing listens: refused at once
	c, err := newClient(masterID, []string{dead, "https://" + live}, peerID.PublicString(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if s, err := c.Status(ctx); err != nil || s.Name != "edge" {
		t.Fatalf("status over the fallback base: %+v %v", s, err)
	}

	// With only the dead base, the call fails rather than hanging forever.
	c2, _ := newClient(masterID, []string{dead}, peerID.PublicString(), 2*time.Second)
	if _, err := c2.Status(ctx); err == nil {
		t.Error("status unexpectedly succeeded with only a dead base")
	}
}
