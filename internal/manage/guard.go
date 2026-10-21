package manage

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/guard"
)

// GuardConfirmWithin is how long the panel has to confirm it still reaches
// the server after turning the firewall on, before the daemon turns it off.
const GuardConfirmWithin = 60 * time.Second

var (
	portKeyRe = regexp.MustCompile(`^(tcp|udp)/(\d{1,5})$`)
	ifaceRe   = regexp.MustCompile(`^[A-Za-z0-9_.@:-]{1,15}$`)
)

// SetGuard saves the firewall's settings. Turning it on from the panel
// (confirm) leaves GuardConfirmWithin for ConfirmGuard; from the terminal it
// stays on, since SSH is never closed.
func SetGuard(g config.Guard, confirm bool) (config.Guard, error) {
	if !guard.ValidICMP(g.ICMP) {
		return g, fmt.Errorf("ping must be closed, iran or open")
	}
	for k, v := range g.Ports {
		m := portKeyRe.FindStringSubmatch(k)
		if m == nil {
			return g, fmt.Errorf("bad port %q (tcp/443 or udp/443)", k)
		}
		if n, _ := strconv.Atoi(m[2]); n < 1 || n > 65535 {
			return g, fmt.Errorf("bad port %q", k)
		}
		if v != config.ScopeAll && v != config.ScopeIran && v != config.ScopeClosed {
			return g, fmt.Errorf("port %s: scope must be all, iran or closed", k)
		}
	}
	clean := func(list []string) ([]string, error) {
		var out []string
		for _, e := range list {
			if e = strings.TrimSpace(e); e == "" {
				continue
			}
			if _, _, err := guard.ParseEntry(e); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	var err error
	if g.Allow, err = clean(g.Allow); err != nil {
		return g, err
	}
	if g.Block, err = clean(g.Block); err != nil {
		return g, err
	}
	for _, n := range g.Ifaces {
		if !ifaceRe.MatchString(n) {
			return g, fmt.Errorf("bad interface name %q", n)
		}
	}
	st, err := config.Update(func(s *config.State) error {
		was := s.Guard != nil && s.Guard.On
		g.ConfirmBy = 0
		if g.On && !was && confirm {
			g.ConfirmBy = time.Now().Add(GuardConfirmWithin).Unix()
		} else if g.On && was && s.Guard.ConfirmBy != 0 {
			g.ConfirmBy = s.Guard.ConfirmBy // still waiting
		}
		s.Guard = &g
		return nil
	})
	if err != nil {
		return g, err
	}
	return *st.Guard, nil
}

// ConfirmGuard records that the panel still reaches the server with the
// firewall on.
func ConfirmGuard() error {
	_, err := config.Update(func(s *config.State) error {
		if s.Guard != nil {
			s.Guard.ConfirmBy = 0
		}
		return nil
	})
	return err
}
