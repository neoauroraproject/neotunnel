package manage

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/guard"
	"neotunnel/internal/identity"
	"neotunnel/internal/mtr"
)

// MTRReport is the path under a tunnel both ways: from this server to the
// far one, and from the far one back (when it is a paired server).
type MTRReport struct {
	There mtr.Result  `json:"there"`
	Back  *mtr.Result `json:"back,omitempty"`
	// BackError is why there is no path back.
	BackError string `json:"back_error,omitempty"`
}

// MTR traces the path under a tunnel from both of its servers at once,
// rounds a second apart, between their public addresses.
func MTR(ctx context.Context, id *identity.Identity, name string, rounds int) (MTRReport, error) {
	st, err := Node()
	if err != nil {
		return MTRReport{}, err
	}
	ts, _ := st.Effective()
	i := slices.IndexFunc(ts, func(t config.Tunnel) bool { return t.Name == name })
	if i < 0 {
		return MTRReport{}, fmt.Errorf("unknown tunnel %q", name)
	}
	// The layer at the bottom carries the packets over the internet.
	t := ts[i]
	for t.Over != "" {
		j := slices.IndexFunc(ts, func(x config.Tunnel) bool { return x.Name == t.Over })
		if j < 0 {
			break
		}
		t = ts[j]
	}
	me := 0
	if st.IsSelf(t.Sides[1].Node) {
		me = 1
	}
	far, err := netip.ParseAddr(t.Sides[1-me].Endpoint)
	if err != nil {
		return MTRReport{}, fmt.Errorf("tunnel %s has no far address to trace to", name)
	}
	here := t.Sides[me].Endpoint
	if here == "" {
		here = st.Address
	}
	var rep MTRReport
	var wg sync.WaitGroup
	if p, ok := st.Peer(t.Sides[1-me].Node); ok && p.Paired() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := agent.ToPeer(id, *p)
			if err == nil {
				var back mtr.Result
				if back, err = c.MTR(ctx, here, rounds); err == nil {
					rep.Back = &back
				}
			}
			if errors.Is(err, agent.ErrUnsupported) {
				err = fmt.Errorf("%s runs an older version without MTR; update it", p.Name)
			}
			if err != nil {
				rep.BackError = err.Error()
			}
		}()
	} else {
		rep.BackError = "the far end isn't a server of this panel"
	}
	rep.There, err = mtr.Run(ctx, far, rounds)
	wg.Wait()
	if err != nil {
		return rep, err
	}
	ir, _ := guard.IR()
	markIR(&rep.There, ir)
	if rep.Back != nil {
		markIR(rep.Back, ir)
	}
	return rep, nil
}

func markIR(r *mtr.Result, ir []netip.Prefix) {
	for i, h := range r.Hops {
		if a, err := netip.ParseAddr(h.Host); err == nil {
			r.Hops[i].IR = slices.ContainsFunc(ir, func(p netip.Prefix) bool { return p.Contains(a) })
		}
	}
}
