package manage

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
	"neotunnel/internal/pmtu"
)

// Mode is a ready-made tunnel stack. The last kind carries the traffic.
type Mode struct {
	Name  string        `json:"name"`
	Title string        `json:"title"`
	Kinds []config.Kind `json:"kinds"`
	Layer int           `json:"layer"`
	Port  bool          `json:"port"`  // can run on a chosen UDP port
	UDP   bool          `json:"udp"`   // always runs on a UDP port
	Crypt bool          `json:"crypt"` // encrypted by itself
	Peer  bool          `json:"peer"`  // needs NeoTunnel on the far end
}

var modes = []Mode{
	{Name: "6to4", Title: "6to4 (SIT + IPIP6)", Kinds: []config.Kind{config.KindSIT, config.KindIPIP6}, Layer: 3, Port: true},
	{Name: "6to4-gre", Title: "6to4 (SIT + GRE6)", Kinds: []config.Kind{config.KindSIT, config.KindIP6GRE}, Layer: 3, Port: true},
	{Name: "ipip", Title: "IPIP", Kinds: []config.Kind{config.KindIPIP}, Layer: 3, Port: true},
	{Name: "gre", Title: "GRE", Kinds: []config.Kind{config.KindGRE}, Layer: 3, Port: true},
	{Name: "sit", Title: "SIT (IPv6 only)", Kinds: []config.Kind{config.KindSIT}, Layer: 3, Port: true},
	{Name: "wireguard", Title: "WireGuard", Kinds: []config.Kind{config.KindWireGuard}, Layer: 3, Port: true, UDP: true, Crypt: true},
	{Name: "obfs", Title: "NeoObfs", Kinds: []config.Kind{config.KindObfs}, Layer: 3, Port: true, UDP: true, Crypt: true, Peer: true},
	{Name: "gretap", Title: "GRETAP (L2)", Kinds: []config.Kind{config.KindGRETAP}, Layer: 2, Port: true},
	{Name: "vxlan", Title: "VXLAN (L2)", Kinds: []config.Kind{config.KindVXLAN}, Layer: 2, Port: true, UDP: true},
	{Name: "geneve", Title: "GENEVE (L2)", Kinds: []config.Kind{config.KindGENEVE}, Layer: 2, Port: true, UDP: true},
	{Name: "l2tpv3", Title: "L2TPv3 (L2)", Kinds: []config.Kind{config.KindL2TPv3}, Layer: 2, Port: true},
	{Name: "eoip", Title: "EoIP (L2)", Kinds: []config.Kind{config.KindEoIP}, Layer: 2, Port: true},
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

// Settings are a tunnel's advanced options. Zero values mean automatic.
type Settings struct {
	MTU       int    `json:"mtu,omitempty"`       // the user-facing tunnel's MTU
	MTUProbe  bool   `json:"mtu_probe,omitempty"` // size from the measured path MTU
	TTL       uint8  `json:"ttl,omitempty"`
	NoDF      bool   `json:"no_df,omitempty"`     // let the path fragment oversized packets
	DSCP      uint8  `json:"dscp,omitempty"`      // outer DSCP class, 0 = inherit
	Encrypt   string `json:"encrypt,omitempty"`   // IPsec cipher on the bottom tunnel
	Padding   int    `json:"padding,omitempty"`   // NeoObfs
	Keepalive int    `json:"keepalive,omitempty"` // WireGuard / NeoObfs
	PeerKey   string `json:"peer_key,omitempty"`  // WireGuard far end not run by NeoTunnel
}

func (s Settings) validate() error {
	if s.MTU != 0 && (s.MTU < 576 || s.MTU > 9000) {
		return fmt.Errorf("MTU must be between 576 and 9000")
	}
	if s.Encrypt != "" {
		if _, err := config.LookupCipher(s.Encrypt); err != nil {
			return err
		}
	}
	if s.Padding < 0 || s.Padding > 255 {
		return fmt.Errorf("padding must be 0-255 bytes")
	}
	if s.DSCP > 63 {
		return fmt.Errorf("DSCP must be 0-63")
	}
	if s.Keepalive < -1 || s.Keepalive > 3600 {
		return fmt.Errorf("keepalive must be -1 (off), 0 (default) or up to 3600 seconds")
	}
	if s.PeerKey != "" {
		if b, err := base64.StdEncoding.DecodeString(s.PeerKey); err != nil || len(b) != 32 {
			return fmt.Errorf("peer key must be a WireGuard public key (base64, 32 bytes)")
		}
	}
	return nil
}

// TunnelRequest creates a tunnel to a managed peer or an unmanaged endpoint.
type TunnelRequest struct {
	Peer   string `json:"peer,omitempty"`
	Remote string `json:"remote,omitempty"` // unmanaged far end (e.g. a MikroTik)
	Mode   string `json:"mode"`
	Name   string `json:"name,omitempty"`
	// Port is the UDP port the tunnel runs on (0 = native protocol; UDP
	// kinds then use their standard or a free port).
	Port uint16 `json:"port,omitempty"`
	// Obfs runs the tunnel over a NeoObfs layer (encrypted, obfuscated UDP).
	Obfs bool `json:"obfs,omitempty"`
	Settings
	// Optional forward created together with the tunnel.
	Forward      string `json:"forward,omitempty"`
	ForwardTo    uint16 `json:"forward_to,omitempty"`
	Proto        string `json:"proto,omitempty"`
	ForwardMode  string `json:"forward_mode,omitempty"`
	ProxyVersion int    `json:"proxy_version,omitempty"`
}

// CreateTunnel builds the mode's stack (carrier first) and its forward.
func CreateTunnel(req TunnelRequest) (config.Tunnel, Change, error) {
	mode, err := LookupMode(req.Mode)
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	if err := req.Settings.validate(); err != nil {
		return config.Tunnel{}, Change{}, err
	}
	if req.Forward != "" {
		if _, err := forward.ParsePorts(req.Forward); err != nil {
			return config.Tunnel{}, Change{}, err
		}
		if err := config.ValidForwardMode(req.ForwardMode, req.ProxyVersion, req.Proto); err != nil {
			return config.Tunnel{}, Change{}, err
		}
	}
	st, err := Node()
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	if req.Remote != "" {
		switch {
		case mode.Peer || req.Obfs:
			return config.Tunnel{}, Change{}, fmt.Errorf("NeoObfs needs NeoTunnel on the far end; choose a managed server")
		case req.Encrypt != "":
			return config.Tunnel{}, Change{}, fmt.Errorf("IPsec keys come from NeoTunnel on both ends; for a router use WireGuard instead")
		case mode.Name == "wireguard" && req.PeerKey == "":
			return config.Tunnel{}, Change{}, fmt.Errorf("give the far end's WireGuard public key (peer key)")
		case req.ForwardMode == config.ForwardTransparent:
			return config.Tunnel{}, Change{}, fmt.Errorf("transparent forwarding needs NeoTunnel on the far end to route replies back")
		}
	}
	if req.Obfs && mode.Crypt {
		req.Obfs = false // already encrypted; a second layer only costs speed
	}
	name := req.Name
	if name == "" {
		name = FreeName(st)
	}
	kinds := mode.Kinds
	if req.Obfs {
		kinds = append([]config.Kind{config.KindObfs}, kinds...)
	}
	if len(kinds) > 1 && len(name) > 8 {
		return config.Tunnel{}, Change{}, fmt.Errorf("name %q is too long for a stacked tunnel (max 8 characters)", name)
	}
	bottomKind, _ := config.LookupKind(kinds[0])
	port := req.Port
	if port == 0 && bottomKind.UDP && bottomKind.Kind != config.KindVXLAN && bottomKind.Kind != config.KindGENEVE {
		port = FreePort(st, bottomKind.Kind)
	}

	var defs []config.Tunnel
	over := ""
	for i, k := range kinds {
		n := name
		switch {
		case i == len(kinds)-1:
		case k == config.KindObfs:
			n = name + "-o"
		default:
			n = name + "-6" // carrier
		}
		opts := TunnelOpts{Kind: string(k), Peer: req.Peer, Remote: req.Remote}
		if i == 0 {
			opts.Port = port // the port belongs to the outer tunnel
		}
		t, err := NewTunnel(st, n, opts)
		if err != nil {
			return config.Tunnel{}, Change{}, err
		}
		if over != "" {
			t.Over = over
			t.Sides[0].Endpoint, t.Sides[1].Endpoint = "", ""
		}
		if i == 0 {
			t.MTUProbe, t.TTL, t.Keepalive, t.Padding, t.PeerKey = req.MTUProbe, req.TTL, req.Keepalive, req.Padding, req.PeerKey
			t.NoDF, t.DSCP = req.NoDF, req.DSCP
			ki, _ := config.LookupKind(k)
			if !ki.Encrypted {
				t.Encrypt = req.Encrypt
			}
		}
		over = n
		defs = append(defs, t)
	}
	top := &defs[len(defs)-1]
	top.Mode, top.MTU = mode.Name, req.MTU

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
			s.Forwards = append(s.Forwards, config.Forward{Node: s.Self, Tunnel: name, Proto: proto, Ports: req.Forward,
				To: req.ForwardTo, Mode: req.ForwardMode, ProxyVersion: req.ProxyVersion})
		}
		return nil
	})
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	t, _ := st.Tunnel(name)
	return *t, ch, nil
}

// UpdateTunnel changes a tunnel's advanced settings. They land on the right
// layer of a stack: MTU on the tunnel itself, everything about the outer
// packets on the bottom one. Port changes the bottom tunnel's UDP port
// (0 keeps it).
func UpdateTunnel(name string, set Settings, port uint16) (Change, error) {
	if err := set.validate(); err != nil {
		return Change{}, err
	}
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return Change{}, fmt.Errorf("unknown tunnel %q", name)
	}
	stack := Stack(st, name)
	bottom, _ := st.Tunnel(stack[0])
	bk, _ := config.LookupKind(bottom.Kind)
	if set.Encrypt != "" && bk.Encrypted {
		set.Encrypt = ""
	}
	if set.Encrypt != "" && bottom.Sides[1].Node == "" {
		return Change{}, fmt.Errorf("IPsec needs NeoTunnel on the far end")
	}
	if port != 0 && port != bottom.Port {
		if !bk.CanUsePort() {
			return Change{}, fmt.Errorf("%s cannot run on a UDP port", bottom.Kind)
		}
		if err := portAvailable(st, port, bottom.Name); err != nil {
			return Change{}, err
		}
	}
	remote, _ := netip.ParseAddr(bottom.Sides[1].Endpoint)
	if !set.MTUProbe && bottom.MTUProbe && remote.IsValid() {
		pmtu.Forget(remote)
	}
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error {
		top, _ := s.Tunnel(name)
		b, _ := s.Tunnel(stack[0])
		if top == nil || b == nil {
			return fmt.Errorf("tunnel %q changed meanwhile", name)
		}
		top.MTU = set.MTU
		b.MTUProbe, b.TTL, b.Encrypt = set.MTUProbe, set.TTL, set.Encrypt
		b.NoDF, b.DSCP = set.NoDF, set.DSCP
		b.Keepalive, b.Padding = set.Keepalive, set.Padding
		if b.Kind == config.KindWireGuard {
			b.PeerKey = set.PeerKey
		}
		if port != 0 {
			b.Port = port
		}
		if b.NeedsSecret() && b.Secret == "" {
			b.Secret = config.NewSecret()
		}
		return nil
	})
	return ch, err
}

// SettingsOf returns a tunnel's current settings and bottom port.
func SettingsOf(st *config.State, name string) (Settings, uint16) {
	stack := Stack(st, name)
	ts, _ := st.Effective()
	find := func(n string) config.Tunnel {
		for _, t := range ts {
			if t.Name == n {
				return t
			}
		}
		return config.Tunnel{}
	}
	top, b := find(name), find(stack[0])
	return Settings{MTU: top.MTU, MTUProbe: b.MTUProbe, TTL: b.TTL, NoDF: b.NoDF, DSCP: b.DSCP, Encrypt: b.Encrypt,
		Padding: b.Padding, Keepalive: b.Keepalive, PeerKey: b.PeerKey}, b.Port
}

// TunnelOpts describes a single tunnel of any kind.
type TunnelOpts struct {
	Kind, Peer, Remote, Over string
	MTU                      int
	Port                     uint16
	VNI                      uint32
	Settings
}

// reservedPorts can't be used as tunnel ports.
var reservedPorts = map[uint16]string{7710: "neotunnel agent", 7711: "neotunnel tests", 22: "SSH"}

func portAvailable(st *config.State, port uint16, self string) error {
	if what, bad := reservedPorts[port]; bad {
		return fmt.Errorf("port %d is reserved for %s", port, what)
	}
	if st.Panel != nil && int(port) == st.Panel.Port {
		return fmt.Errorf("port %d is the panel's port", port)
	}
	if other := portUser(st, port); other != "" && other != self {
		return fmt.Errorf("port %d is already used by tunnel %s", port, other)
	}
	return nil
}

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
	if err := o.Settings.validate(); err != nil {
		return config.Tunnel{}, err
	}
	t := config.Tunnel{Name: name, Kind: kind, Over: o.Over, MTU: o.MTU, Port: o.Port, VNI: o.VNI,
		MTUProbe: o.MTUProbe, TTL: o.TTL, NoDF: o.NoDF, DSCP: o.DSCP, Encrypt: o.Encrypt, Padding: o.Padding, Keepalive: o.Keepalive, PeerKey: o.PeerKey}
	if o.MTU == 0 {
		t.MTU = o.Settings.MTU
	}
	if ki.Encrypted {
		t.Encrypt = ""
	}
	if o.Port != 0 {
		if !ki.CanUsePort() {
			return t, fmt.Errorf("%s cannot run on a UDP port", kind)
		}
		if err := portAvailable(st, o.Port, ""); err != nil {
			return t, err
		}
	} else if ki.UDP && kind != config.KindVXLAN && kind != config.KindGENEVE && o.Over == "" {
		t.Port = FreePort(st, kind)
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
		if ki.NeedsPeer {
			return t, fmt.Errorf("%s needs NeoTunnel on the far end", kind)
		}
		if t.Encrypt != "" {
			return t, fmt.Errorf("IPsec needs NeoTunnel on the far end")
		}
		if kind == config.KindWireGuard && t.PeerKey == "" {
			return t, fmt.Errorf("give the far end's WireGuard public key (--peer-key)")
		}
		t.Sides = [2]config.Side{self, {Endpoint: o.Remote}}
	default:
		return t, fmt.Errorf("choose a server or a remote IP")
	}
	if other := keylessConflict(st, t); other != "" {
		return t, fmt.Errorf("tunnel %s already runs %s between these two servers and the kernel allows only one: give this one a UDP port, or pick GRE or VXLAN", other, kind)
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
		t.Encrypt, t.MTUProbe = "", false
	}
	return t, nil
}

// keylessConflict reports a tunnel the kernel can't tell apart from t: SIT,
// IPIP and ip6tnl tunnels have no key, so only one of each type may run
// between the same two addresses unless one of them is on a UDP port.
func keylessConflict(st *config.State, t config.Tunnel) string {
	group := func(k config.Kind) string {
		switch k {
		case config.KindSIT, config.KindIPIP:
			return string(k)
		case config.KindIPIP6, config.KindIP6IP6:
			return "ip6tnl"
		}
		return ""
	}
	g := group(t.Kind)
	if g == "" || t.Over != "" || t.Port != 0 {
		return ""
	}
	ts, _ := st.Effective()
	for _, o := range ts {
		if o.Name != t.Name && o.Over == "" && o.Port == 0 && group(o.Kind) == g &&
			((o.Sides[1].Endpoint == t.Sides[1].Endpoint && o.Sides[0].Endpoint == t.Sides[0].Endpoint) ||
				(o.Sides[0].Endpoint == t.Sides[1].Endpoint && o.Sides[1].Endpoint == t.Sides[0].Endpoint)) {
			return o.Name
		}
	}
	return ""
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

// FreePort picks an unused UDP port for a kind that always needs one.
func FreePort(st *config.State, kind config.Kind) uint16 {
	start := uint16(51820)
	if kind == config.KindObfs {
		start = 30443
	}
	for p := start; p < 65000; p++ {
		if portAvailable(st, p, "") == nil {
			return p
		}
	}
	return start
}

// FreeName returns the first unused default tunnel name (t1, t2, ...).
func FreeName(st *config.State) string {
	for i := 1; ; i++ {
		n := fmt.Sprintf("t%d", i)
		_, a := st.Tunnel(n)
		_, b := st.Tunnel(n + "-6")
		_, c := st.Tunnel(n + "-o")
		if !a && !b && !c {
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

// RemoveTunnel removes a tunnel, everything stacked on it and the layers
// under it that nothing else uses, on both ends.
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
	// Removing the user-facing tunnel of a stack also removes its carriers.
	for under := t.Over; under != ""; {
		if len(Dependents(st, under)) != len(drop)+1 {
			break
		}
		drop = append(drop, under)
		ut, ok := st.Tunnel(under)
		if !ok {
			break
		}
		under = ut.Over
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
	ts, _ := st.Effective()
	byName := map[string]config.Tunnel{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	var out []string
	for n := name; n != "" && len(out) < 5; {
		out = append([]string{n}, out...)
		t, ok := byName[n]
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
