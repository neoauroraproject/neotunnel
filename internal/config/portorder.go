package config

import "slices"

// PortKey identifies a forward in the panel's order of ports: its protocol
// and ports, which stay when it moves to another tunnel.
func (f Forward) PortKey() string { return f.Proto + "/" + f.Ports }

// SortForwards returns fs in the order the user arranged them; forwards not
// in the order keep their relative place after those that are.
func (s *State) SortForwards(fs []Forward) []Forward {
	out := slices.Clone(fs)
	if len(s.PortOrder) == 0 {
		return out
	}
	rank := make(map[string]int, len(s.PortOrder))
	for i, k := range s.PortOrder {
		rank[k] = i
	}
	pos := func(f Forward) int {
		if r, ok := rank[f.PortKey()]; ok {
			return r
		}
		return len(rank)
	}
	slices.SortStableFunc(out, func(a, b Forward) int { return pos(a) - pos(b) })
	return out
}
