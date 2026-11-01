package manage

import (
	"fmt"
	"net/netip"
	"slices"

	"neotunnel/internal/config"
)

// PoolChoice says where a new tunnel's inner addresses come from: ranges
// given by hand (Pool4, Pool6), or a pool by name, or the default pool in the
// settings. A family with no range uses the built-in one.
type PoolChoice struct {
	Pool  string `json:"pool,omitempty"`
	Pool4 string `json:"pool4,omitempty"`
	Pool6 string `json:"pool6,omitempty"`
}

// ranges returns the ranges a tunnel carves from ("" = the built-in one).
func (c PoolChoice) ranges(st *config.State) (string, string, error) {
	if c.Pool4 != "" || c.Pool6 != "" {
		n4, err := config.ValidPoolRange(c.Pool4, 4)
		if err != nil {
			return "", "", err
		}
		n6, err := config.ValidPoolRange(c.Pool6, 6)
		return n4, n6, err
	}
	name := c.Pool
	if name == "" {
		name = st.D().Pool
		if _, ok := st.LookupPool(name); !ok {
			return "", "", nil
		}
	}
	if name == "" || name == config.DefaultPool {
		return "", "", nil
	}
	p, ok := st.LookupPool(name)
	if !ok {
		return "", "", fmt.Errorf("unknown address pool %q", name)
	}
	return p.Net4, p.Net6, nil
}

// checkPoolAddrs keeps a tunnel's ranges off the addresses it runs between:
// a route to them through the tunnel itself would cut the tunnel.
func checkPoolAddrs(t config.Tunnel) error {
	for _, r := range []string{t.Pool4, t.Pool6} {
		p, err := netip.ParsePrefix(r)
		if err != nil {
			continue
		}
		for _, sd := range t.Sides {
			if a, err := netip.ParseAddr(sd.Endpoint); err == nil && p.Contains(a.Unmap()) {
				return fmt.Errorf("range %s contains the server address %s; choose another", r, sd.Endpoint)
			}
		}
	}
	return nil
}

// SavePool adds an address pool, or changes the one named was. Tunnels keep
// the addresses they have; only new tunnels use the new ranges.
func SavePool(p config.AddressPool, was string) error {
	if err := config.ValidPoolName(p.Name); err != nil {
		return err
	}
	if err := p.Check(); err != nil {
		return err
	}
	if config.BuiltinPool(p.Name) || config.BuiltinPool(was) {
		return fmt.Errorf("%q is a ready-made pool and can't be changed; give yours another name", p.Name)
	}
	_, err := config.Update(func(s *config.State) error {
		at := -1
		for i, o := range s.Pools {
			switch {
			case was != "" && o.Name == was:
				at = i
			case o.Name == p.Name:
				return fmt.Errorf("a pool named %q already exists", p.Name)
			}
		}
		if at < 0 {
			if was != "" {
				return fmt.Errorf("unknown address pool %q", was)
			}
			s.Pools = append(s.Pools, p)
			return nil
		}
		s.Pools[at] = p
		if s.Defaults != nil && s.Defaults.Pool == was {
			s.Defaults.Pool = p.Name
		}
		return nil
	})
	return err
}

// RemovePool forgets an address pool; tunnels made from it keep their
// addresses and the range they came from.
func RemovePool(name string) error {
	_, err := config.Update(func(s *config.State) error {
		n := len(s.Pools)
		s.Pools = slices.DeleteFunc(s.Pools, func(p config.AddressPool) bool { return p.Name == name })
		if len(s.Pools) == n {
			if config.BuiltinPool(name) {
				return fmt.Errorf("%q is a ready-made pool and can't be removed", name)
			}
			return fmt.Errorf("unknown address pool %q", name)
		}
		if s.Defaults != nil && s.Defaults.Pool == name {
			s.Defaults.Pool = ""
		}
		return nil
	})
	return err
}

// SetPortOrder saves the order ports are listed in, by config.Forward.PortKey.
// It changes nothing on the network, so nothing is applied or pushed.
func SetPortOrder(keys []string) error {
	if len(keys) > 10000 {
		return fmt.Errorf("too many ports")
	}
	_, err := config.Update(func(s *config.State) error {
		s.PortOrder = slices.Compact(slices.Clone(keys))
		return nil
	})
	return err
}
