package manage

import (
	"fmt"
	"slices"

	"neotunnel/internal/config"
)

// FetchTunnels lists the tunnels this node's downloads can leave through:
// its own, to a paired NeoTunnel server, and turned on.
func FetchTunnels(st *config.State) []string {
	// The layers under a stack (6to4's SIT) aren't tunnels users pick.
	carriers := map[string]bool{}
	for _, t := range st.Tunnels {
		carriers[t.Over] = true
	}
	var out []string
	for _, t := range st.Tunnels {
		if !carriers[t.Name] && fetchTunnelOK(st, t) == nil {
			out = append(out, t.Name)
		}
	}
	return out
}

func fetchTunnelOK(st *config.State, t config.Tunnel) error {
	if t.Disabled {
		return fmt.Errorf("tunnel %s is turned off", t.Name)
	}
	for _, n := range st.PeersOf(t) {
		if p, ok := st.Peer(n); ok && p.Paired() {
			return nil
		}
	}
	return fmt.Errorf("tunnel %s doesn't go to a NeoTunnel server connected to this one", t.Name)
}

// SetFetch saves the downloads-through-a-tunnel settings and tells the far
// servers (the old tunnel's and the new one's) to let them out, or not.
func SetFetch(f config.Fetch) (Change, error) {
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	for _, g := range f.Groups {
		if !slices.ContainsFunc(config.FetchGroups, func(x config.FetchGroup) bool { return x.Name == g }) {
			return Change{}, fmt.Errorf("unknown group %q", g)
		}
	}
	var domains []string
	for _, d := range f.Domains {
		d = config.CleanDomain(d)
		if d == "" || slices.Contains(domains, d) {
			continue
		}
		if err := config.ValidDomain(d); err != nil {
			return Change{}, err
		}
		domains = append(domains, d)
	}
	f.Domains = domains
	var peers []string
	if f.Tunnel != "" {
		t, ok := st.Tunnel(f.Tunnel)
		if !ok {
			return Change{}, fmt.Errorf("unknown tunnel %q (only this server's own tunnels can carry its downloads)", f.Tunnel)
		}
		if f.On {
			if err := fetchTunnelOK(st, *t); err != nil {
				return Change{}, err
			}
		}
		peers = st.PeersOf(*t)
	}
	if f.On && f.Tunnel == "" {
		return Change{}, fmt.Errorf("choose the tunnel the downloads go through")
	}
	if f.On && len(f.Sites()) == 0 {
		return Change{}, fmt.Errorf("choose at least one site")
	}
	if old, ok := st.FetchActive(); ok {
		if t, ok := st.Tunnel(old.Tunnel); ok {
			peers = append(peers, st.PeersOf(*t)...)
		}
	}
	_, ch, err := Commit(peers, func(s *config.State) error {
		if t, ok := s.Tunnel(f.Tunnel); ok && f.On {
			relaySecret(t, config.ForwardSocks)
		}
		s.Fetch = &f
		return nil
	})
	return ch, err
}
