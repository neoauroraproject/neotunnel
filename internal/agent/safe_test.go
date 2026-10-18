package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/safe"
)

// The master keeps a safe change on its peer; a change the peer doesn't hold
// (never received, or already put back) is refused, and old peers say so.
func TestSafeConfirm(t *testing.T) {
	masterID := newIdentity(t)
	peerID := newIdentity(t) // config.Dir now points at the peer
	m := &config.Managed{Master: config.MasterRef{Name: "hub", PublicKey: masterID.PublicString()}, Self: "edge", Revision: 3}
	config.Save(&config.State{Self: "edge", Managed: m})
	addr := serve(t, peerID)
	c, err := newClient(masterID, addr, peerID.PublicString(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := safe.HoldManaged("c1", 75, m, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := c.SafeConfirm(ctx, "other"); err == nil {
		t.Fatal("confirmed a change that isn't held")
	}
	if err := c.SafeConfirm(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SafeConfirm(ctx, "c1"); err == nil {
		t.Fatal("confirmed twice")
	}
	if err := c.do(ctx, "POST", "/v1/not-there", nil, nil, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown call: %v", err)
	}
}
