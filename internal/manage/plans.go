package manage

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
)

// SetPortPlan sells the forward of ports on tunnel as a product, or stops
// selling it (p nil). A new plan's quota period starts now; changing one
// keeps its period unless restart is set. Only this node enforces it, so
// nothing goes to the peers.
func SetPortPlan(tunnel, ports string, p *config.PortPlan, restart bool) (Change, error) {
	if p != nil {
		if err := checkPlan(p); err != nil {
			return Change{}, err
		}
	}
	now := time.Now().Unix()
	_, ch, err := Commit(nil, func(s *config.State) error {
		f := ownForward(s, tunnel, ports)
		if f == nil {
			return fmt.Errorf("no forward of %s on %s", ports, tunnel)
		}
		if p == nil {
			f.Plan = nil
			return nil
		}
		np := *p
		np.Customer = strings.TrimSpace(np.Customer)
		np.Start = now
		if f.Plan != nil && f.Plan.Start != 0 && !restart {
			np.Start = f.Plan.Start
		}
		f.Plan = &np
		return nil
	})
	if err == nil {
		events.Add(events.Event{Tunnel: tunnel, Kind: events.Changed, Detail: "plan " + ports})
	}
	return ch, err
}

// RenewPort opens a sold port if paused and moves its end days on: from
// the end, or from now once that has passed (days 0 leaves the end alone).
// Its traffic used stays counted against the quota unless reset is set,
// which starts a new period now with the quota whole again.
func RenewPort(tunnel, ports string, days int, reset bool) (Change, error) {
	if days < 0 || days > 3650 {
		return Change{}, fmt.Errorf("renew for 0 to 3650 days")
	}
	now := time.Now().Unix()
	_, ch, err := Commit(nil, func(s *config.State) error {
		f := ownForward(s, tunnel, ports)
		if f == nil {
			return fmt.Errorf("no forward of %s on %s", ports, tunnel)
		}
		if f.Plan == nil {
			return fmt.Errorf("port %s isn't sold: set its plan first", ports)
		}
		np := *f.Plan
		np.Paused = false
		if reset {
			np.Start = now
		}
		if days > 0 {
			np.Expires = max(np.Expires, now) + int64(days)*86400
		}
		f.Plan = &np
		return nil
	})
	if err == nil {
		events.Add(events.Event{Tunnel: tunnel, Kind: events.Changed, Detail: "renew " + ports})
	}
	return ch, err
}

// ownForward finds this node's forward of ports on tunnel.
func ownForward(s *config.State, tunnel, ports string) *config.Forward {
	for i, f := range s.Forwards {
		if f.Node == s.Self && f.Tunnel == tunnel && f.Ports == ports {
			return &s.Forwards[i]
		}
	}
	return nil
}

func checkPlan(p *config.PortPlan) error {
	switch {
	case utf8.RuneCountInString(p.Customer) > 100:
		return fmt.Errorf("the customer's name is up to 100 characters")
	case p.RateMbit < 0 || p.RateMbit > 100_000:
		return fmt.Errorf("speed cap must be 0 (none) to 100000 Mbit/s")
	case p.QuotaGB < 0 || p.QuotaGB > 1_000_000:
		return fmt.Errorf("quota must be 0 (none) to 1000000 GB")
	case p.ResetDays < 0 || p.ResetDays > 3650:
		return fmt.Errorf("the quota renews every 0 (never) to 3650 days")
	case p.Expires < 0:
		return fmt.Errorf("bad end date")
	case p.MaxConns < 0 || p.MaxConns > 1_000_000:
		return fmt.Errorf("connections must be 0 (no limit) to 1000000")
	case p.MaxIPs < 0 || p.MaxIPs > 65_535:
		return fmt.Errorf("client addresses must be 0 (no limit) to 65535")
	}
	return nil
}
