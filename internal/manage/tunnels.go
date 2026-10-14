package manage

import (
	"fmt"
	"net/netip"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
)

// Mode is a ready-made tunnel stack. The last kind carries the traffic.
type Mode struct {
	Name  string        `json:"name"`
	Title string        `json:"title"`
	Kinds []config.Kind `json:"kinds"`
	Layer int           `json:"layer"`
	Port  bool          `json:"port"` // can run on a chosen UDP port
}

var modes = []Mode{
	{"6to4", "6to4 (SIT + IPIP6)", []config.Kind{config.KindSIT, config.KindIPIP6}, 3, true},
	{"6to4-gre", "6to4 (SIT + GRE6)", []config.Kind{config.KindSIT, config.KindIP6GRE}, 3, true},
	{"ipip", "IPIP", []config.Kind{config.KindIPIP}, 3, true},
	{"gre", "GRE", []config.Kind{config.KindGRE}, 3, true},
	{"sit", "SIT (IPv6 only)", []config.Kind{config.KindSIT}, 3, true},
	{"gretap", "GRETAP (L2)", []config.Kind{config.KindGRETAP}, 2, true},
	{"vxlan", "VXLAN (L2)", []config.Kind{config.KindVXLAN}, 2, true},
	{"geneve", "GENEVE (L2)", []config.Kind{config.KindGENEVE}, 2, true},
}

func Modes() []Mode { return modes }

func ModeNames() []string {
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = m.Name
	}
	return out
}

func LookupMode(name string) (Mode, error) {
	for _, m := range modes {
		if m.Name == name {
			return m, nil
		}
	}
	return Mode{}, fmt.Errorf("unknown mode %q (choose: %s)", name, strings.Join(ModeNames(), ", "))
}

// TunnelRequest creates a tunnel to a managed peer or an unmanaged endpoint.
type TunnelRequest struct {
	Peer   string `json:"peer,omitempty"`
	Remote string `json:"remote,omitempty"` // unmanaged far end (e.g. a MikroTik)
	Mode   string `json:"mode"`
	Name   string `json:"name,omitempty"`
	// Port is the UDP port the tunnel runs on (0 = native protocol; VXLAN and
	// GENEVE then use their standard port).
	Port uint16 `json:"port,omitempty"`
	// Optional forward created together with the tunnel.
	Forward   string `json:"forward,omitempty"`
	ForwardTo uint16 `json:"forward_to,omitempty"`
	Proto     string `json:"proto,omitempty"`
}

// CreateTunnel builds the mode's stack (carrier first) and its forward.
func CreateTunnel(req TunnelRequest) (config.Tunnel, Change, error) {
	mode, err := LookupMode(req.Mode)
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	if req.Forward != "" {
		if _, err := forward.ParsePorts(req.Forward); err != nil {
			return config.Tunnel{}, Change{}, err
		}
	}
	st, err := Node()
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	name := req.Name
	if name == "" {
		name = freeName(st)
	}
	if len(mode.Kinds) > 1 && len(name) > 8 {
		return config.Tunnel{}, Change{}, fmt.Errorf("name %q is too long for a stacked tunnel (max 8 characters)", name)
	}

	var defs []config.Tunnel
	over := ""
	for i, k := range mode.Kinds {
		n := name
		if i < len(mode.Kinds)-1 {
			n = name + "-6" // carrier
		}
		opts := TunnelOpts{Kind: string(k), Peer: req.Peer, Remote: req.Remote}
		if i == 0 {
			opts.Port = req.Port // the port belongs to the outer tunnel
		}
		t, err := NewTunnel(st, n, opts)
		if err != nil {
			return config.Tunnel{}, Change{}, err
		}
		if over != "" {
			t.Over = over
			t.Sides[0].Endpoint, t.Sides[1].Endpoint = "", ""
		}
		over = n
		defs = append(defs, t)
	}
	defs[len(defs)-1].Mode = mode.Name

	var peers []string
	if req.Peer != "" {
		peers = []string{req.Peer}
	}
	st, ch, err := Commit(peers, func(s *config.State) error {
		for i := range defs {
			if _, dup := s.Tunnel(defs[i].Name); dup {
				return fmt.Errorf("tunnel %q already exists", defs[i].Name)
			}
			if err := s.Allocate(&defs[i]); err != nil {
				return err
			}
			s.Tunnels = append(s.Tunnels, defs[i])
		}
		if req.Forward != "" {
			proto := req.Proto
			if proto == "" {
				proto = "both"
			}
			if err := portsFree(s, req.Forward, proto, ""); err != nil {
				return err
			}
			s.Forwards = append(s.Forwards, config.Forward{Node: s.Self, Tunnel: name, Proto: proto, Ports: req.Forward, To: req.ForwardTo})
		}
		return nil
	})
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	t, _ := st.Tunnel(name)
	return *t, ch, nil
}

// TunnelOpts describes a single tunnel of any kind.
type TunnelOpts struct {
	Kind, Peer, Remote, Over string
	MTU                      int
	Port                     uint16
	VNI                      uint32
}

// reservedPorts can't be used as tunnel ports.
var reservedPorts = map[uint16]string{7710: "neotunnel agent", 7711: "neotunnel tests", 22: "SSH"}

// NewTunnel validates and builds one tunnel definition from this node to a
// peer or remote endpoint (not yet saved).
func NewTunnel(st *config.State, name string, o TunnelOpts) (config.Tunnel, error) {
	if err := config.ValidName(name); err != nil {
		return config.Tunnel{}, err
	}
	kind := config.Kind(strings.ToLower(o.Kind))
	ki, err := config.LookupKind(kind)
	if err != nil {
		return config.Tunnel{}, err
	}
	t := config.Tunnel{Name: name, Kind: kind, Over: o.Over, MTU: o.MTU, Port: o.Port, VNI: o.VNI}
	if o.Port != 0 {
		if !ki.CanUsePort() {
			return t, fmt.Errorf("%s cannot run on a UDP port", kind)
		}
		if what, bad := reservedPorts[o.Port]; bad {
			return t, fmt.Errorf("port %d is reserved for %s", o.Port, what)
		}
		if st.Panel != nil && int(o.Port) == st.Panel.Port {
			return t, fmt.Errorf("port %d is the panel's port", o.Port)
		}
		if other := portUser(st, o.Port); other != "" {
			return t, fmt.Errorf("port %d is already used by tunnel %s", o.Port, other)
		}
	}
	self := config.Side{Node: st.Self, Endpoint: st.Address}
	switch {
	case o.Peer != "" && o.Remote != "":
		return t, fmt.Errorf("choose a managed server or a remote IP, not both")
	case o.Peer != "":
		p, ok := st.Peer(o.Peer)
		if !ok {
			return t, fmt.Errorf("unknown server %q", o.Peer)
		}
		t.Sides = [2]config.Side{self, {Node: p.Name, Endpoint: p.Address}}
	case o.Remote != "":
		if _, err := netip.ParseAddr(o.Remote); err != nil {
			return t, fmt.Errorf("remote must be an IP: %w", err)
		}
		t.Sides = [2]config.Side{self, {Endpoint: o.Remote}}
	default:
		return t, fmt.Errorf("choose a server or a remote IP")
	}
	if o.Over != "" {
		parent, ok := st.Tunnel(o.Over)
		if !ok {
			return t, fmt.Errorf("unknown tunnel %q to run over", o.Over)
		}
		if parent.Sides[1].Node != t.Sides[1].Node || parent.Sides[1].Endpoint != t.Sides[1].Endpoint {
			return t, fmt.Errorf("tunnel %s goes to a different server", o.Over)
		}
		// The underlay comes from the parent's inner addresses.
		t.Sides[0].Endpoint, t.Sides[1].Endpoint = "", ""
	}
	return t, nil
}

// portUser returns the tunnel on this node already running on port.
func portUser(st *config.State, port uint16) string {
	ts, _ := st.Effective()
	for _, t := range ts {
		if t.Port == port {
			return t.Name
		}
	}
	return ""
}

func freeName(st *config.State) string {
	for i := 1; ; i++ {
		n := fmt.Sprintf("t%d", i)
		_, a := st.Tunnel(n)
		_, b := st.Tunnel(n + "-6")
		if !a && !b {
			return n
		}
	}
}

// AddTunnel saves a single tunnel built with NewTunnel.
func AddTunnel(t config.Tunnel) (config.Tunnel, Change, error) {
	pre, err := Node()
	if err != nil {
		return t, Change{}, err
	}
	st, ch, err := Commit(pre.PeersOf(t), func(s *config.State) error {
		if _, dup := s.Tunnel(t.Name); dup {
			return fmt.Errorf("tunnel %q already exists", t.Name)
		}
		if err := s.Allocate(&t); err != nil {
			return err
		}
		s.Tunnels = append(s.Tunnels, t)
		return nil
	})
	if err != nil {
		return t, Change{}, err
	}
	saved, _ := st.Tunnel(t.Name)
	return *saved, ch, nil
}

// RemoveTunnel removes a tunnel, everything stacked on it and its carrier if
// nothing else uses it, on both ends.
func RemoveTunnel(name string) ([]string, Change, error) {
	st, err := Node()
	if err != nil {
		return nil, Change{}, err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		if st.Managed != nil {
			return nil, Change{}, fmt.Errorf("tunnel %q is managed by %s; change it there", name, st.Managed.Master.Name)
		}
		return nil, Change{}, fmt.Errorf("unknown tunnel %q", name)
	}
	drop := Dependents(st, name)
	// Removing the user-facing tunnel of a stack also removes its carrier.
	if t.Over != "" && len(Dependents(st, t.Over)) == 2 {
		drop = append(drop, t.Over)
	}
	var peers []string
	for _, n := range drop {
		if dt, ok := st.Tunnel(n); ok {
			peers = append(peers, st.PeersOf(*dt)...)
		}
	}
	_, ch, err := Commit(peers, func(s *config.State) error {
		removeTunnels(s, drop)
		return nil
	})
	return drop, ch, err
}

// Stack returns name and the tunnels it runs over, underlay first.
func Stack(st *config.State, name string) []string {
	var out []string
	for n := name; n != "" && len(out) < 5; {
		out = append([]string{n}, out...)
		t, ok := st.Tunnel(n)
		if !ok {
			break
		}
		n = t.Over
	}
	return out
}

// Dependents returns name plus every tunnel stacked on it.
func Dependents(st *config.State, name string) []string {
	out := []string{name}
	for i := 0; i < len(out); i++ {
		for _, t := range st.Tunnels {
			if t.Over == out[i] {
				out = append(out, t.Name)
			}
		}
	}
	return out
}

func removeTunnels(s *config.State, names []string) {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	var ts []config.Tunnel
	for _, t := range s.Tunnels {
		if !drop[t.Name] {
			ts = append(ts, t)
		}
	}
	s.Tunnels = ts
	var fs []config.Forward
	for _, f := range s.Forwards {
		if !drop[f.Tunnel] {
			fs = append(fs, f)
		}
	}
	s.Forwards = fs
}
