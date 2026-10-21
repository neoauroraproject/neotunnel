package manage

import (
	"context"
	"fmt"
	"strings"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
)

// RestartTunnel takes a tunnel and the layers under it down and up again,
// on this node and on the far server, without changing any setting. The
// panel's restart button and the daemon's recovery both use it. It returns
// the far server's error, if any; this node is always restarted.
func RestartTunnel(ctx context.Context, id *identity.Identity, name string) error {
	st, err := Node()
	if err != nil {
		return err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	names := Stack(st, name)
	var peerErr error
	if p, ok := st.Peer(t.Sides[1].Node); ok && p.Paired() {
		if c, err := agent.ToPeer(id, *p); err != nil {
			peerErr = err
		} else if err := c.Restart(ctx, names); err != nil && !strings.Contains(err.Error(), "404") {
			peerErr = fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	engine.Restart(names, func() {
		if cur, err := config.Load(); err == nil {
			engine.Apply(cur)
		}
	})
	return peerErr
}
