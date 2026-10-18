package manage

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
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
	TCP   bool          `json:"tcp"`   // always runs on a TCP port
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
	// TCP is the last resort for paths that pass neither UDP nor raw IP.
	{Name: "tcp", Title: "TCP", Kinds: []config.Kind{config.KindTCP}, Layer: 3, Port: true, TCP: true, Peer: true},
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
	// TunnelID is EoIP's tunnel id (nil = derived from the name); it must
	// match a MikroTik far end's tunnel-id.
	TunnelID *uint16 `json:"tunnel_id,omitempty"`
	// Profile: lean, balanced ("") or max; see config.ProfileMax.
	Profile string `json:"profile,omitempty"`
	// Lanes runs the tunnel on this many parallel GRE tunnels (0 or 1 = one,
	// up to config.MaxLanes), so its flows spread over CPU cores.
	Lanes int `json:"lanes,omitempty"`
}

func (s Settings) validate() error {
	if !config.ValidProfile(s.Profile) {
		return fmt.Errorf("profile must be lean, balanced or max")
	}
	if s.Lanes < 0 || s.Lanes > config.MaxLanes {
		return fmt.Errorf("lanes must be 1-%d", config.MaxLanes)
	}
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
	// Underlay "ipv6" runs the tunnel between the two servers' IPv6
	// addresses (Local6 here, Remote6 there) instead of IPv4: GRE, IPIP and
	// SIT become their IPv6 forms, UDP kinds simply use IPv6.
	Underlay string `json:"underlay,omitempty"`
	Local6   string `json:"local6,omitempty"`
	Remote6  string `json:"remote6,omitempty"`
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
	if req.Forward != "" {
		if req.Proto == "" && req.ForwardMode == config.ForwardRelay {
			req.Proto = "tcp"
		}
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
	name, defs, err := PlanTunnel(st, req)
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
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
			if t, ok := s.Tunnel(name); ok {
				relaySecret(t, req.ForwardMode)
			}
		}
		return nil
	})
	if err != nil {
		return config.Tunnel{}, Change{}, err
	}
	t, _ := st.Tunnel(name)
	return *t, ch, nil
}

// under6 maps a stack to its IPv6-underlay form.
func under6(kinds []config.Kind) ([]config.Kind, error) {
	if len(kinds) > 1 && kinds[0] == config.KindSIT {
		return nil, fmt.Errorf("6to4 is IPv6 carried over IPv4; over IPv6 pick GRE, IPIP or a UDP kind")
	}
	out := append([]config.Kind(nil), kinds...)
	switch out[0] {
	case config.KindGRE:
		out[0] = config.KindIP6GRE
	case config.KindGRETAP:
		out[0] = config.KindIP6GRETAP
	case config.KindIPIP:
		out[0] = config.KindIPIP6
	case config.KindSIT:
		out[0] = config.KindIP6IP6
	}
	return out, nil
}

// PlanTunnel checks a request and returns the tunnels it creates, bottom
// first, with names but not yet allocated or saved.
func PlanTunnel(st *config.State, req TunnelRequest) (string, []config.Tunnel, error) {
	mode, err := LookupMode(req.Mode)
	if err != nil {
		return "", nil, err
	}
	if err := req.Settings.validate(); err != nil {
		return "", nil, err
	}
	var local6, remote6 netip.Addr
	if v6 := req.Underlay == "ipv6"; v6 {
		local6, err = netip.ParseAddr(req.Local6)
		if err != nil || !local6.Is6() || local6.Is4In6() {
			return "", nil, fmt.Errorf("give this server's IPv6 address for an IPv6 underlay")
		}
		remote6, err = netip.ParseAddr(req.Remote6)
		if err != nil || !remote6.Is6() || remote6.Is4In6() {
			return "", nil, fmt.Errorf("give the far end's IPv6 address for an IPv6 underlay")
		}
	} else if req.Underlay != "" && req.Underlay != "ipv4" {
		return "", nil, fmt.Errorf("underlay must be ipv4 or ipv6")
	}
	if req.Remote != "" {
		switch {
		case mode.Peer || req.Obfs:
			what := mode.Title
			if req.Obfs {
				what = "NeoObfs"
			}
			return "", nil, fmt.Errorf("%s needs NeoTunnel on the far end; choose a managed server", what)
		case req.Encrypt != "":
			return "", nil, fmt.Errorf("IPsec keys come from NeoTunnel on both ends; for a router use WireGuard instead")
		case mode.Name == "wireguard" && req.PeerKey == "":
			return "", nil, fmt.Errorf("give the far end's WireGuard public key (peer key)")
		case req.ForwardMode == config.ForwardTransparent:
			return "", nil, fmt.Errorf("transparent forwarding needs NeoTunnel on the far end to route replies back")
		case req.ForwardMode == config.ForwardRelay:
			return "", nil, fmt.Errorf("relay mode needs NeoTunnel on the far end to accept the connections")
		}
	}
	if req.Obfs && mode.TCP {
		return "", nil, fmt.Errorf("NeoObfs is UDP; it can't run under a TCP tunnel")
	}
	if req.Obfs && mode.Crypt {
		req.Obfs = false // already encrypted; a second layer only costs speed
	}
	name := req.Name
	if name == "" {
		name = FreeName(st)
	}
	kinds := mode.Kinds
	if local6.IsValid() {
		if kinds, err = under6(kinds); err != nil {
			return "", nil, err
		}
	}
	if req.Obfs {
		kinds = append([]config.Kind{config.KindObfs}, kinds...)
	}
	if len(kinds) > 1 && len(name) > 8 {
		return "", nil, fmt.Errorf("name %q is too long for a stacked tunnel (max 8 characters)", name)
	}
	bottomKind, _ := config.LookupKind(kinds[0])
	port := req.Port
	if port == 0 && (bottomKind.UDP || bottomKind.TCP) && bottomKind.Kind != config.KindVXLAN && bottomKind.Kind != config.KindGENEVE {
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
		opts := TunnelOpts{Kind: string(k), Peer: req.Peer, Remote: req.Remote, stacked: i > 0}
		if i == 0 {
			opts.Port = port // the port belongs to the outer tunnel
		}
		t, err := NewTunnel(st, n, opts)
		if err != nil {
			return "", nil, err
		}
		if over != "" {
			t.Over = over
			t.Sides[0].Endpoint, t.Sides[1].Endpoint = "", ""
		}
		if i == 0 && local6.IsValid() {
			if ki, _ := config.LookupKind(k); ki.FOU && port != 0 {
				return "", nil, fmt.Errorf("a UDP port (FOU) needs an IPv4 underlay; over IPv6 run %s without a port", k)
			}
			t.Sides[0].Endpoint, t.Sides[1].Endpoint = local6.String(), remote6.String()
		}
		if i == 0 {
			t.MTUProbe, t.TTL, t.Keepalive, t.Padding, t.PeerKey = req.MTUProbe, req.TTL, req.Keepalive, req.Padding, req.PeerKey
			t.NoDF, t.DSCP = req.NoDF, req.DSCP
			ki, _ := config.LookupKind(k)
			if !ki.Encrypted {
				t.Encrypt = req.Encrypt
			}
		}
		if k == config.KindEoIP {
			t.TunnelID = req.TunnelID
			if err := checkTunnelID(t); err != nil {
				return "", nil, err
			}
		}
		over = n
		defs = append(defs, t)
	}
	top := &defs[len(defs)-1]
	top.Mode, top.MTU, top.Profile = mode.Name, req.MTU, profileOf(req.Profile)
	top.Lanes = lanesOf(req.Lanes)
	if err := checkLanes(*top); err != nil {
		return "", nil, err
	}
	return name, defs, nil
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
	if t.Kind == config.KindEoIP {
		c := *t
		c.TunnelID = set.TunnelID
		if t.Name == bottom.Name && port != 0 {
			c.Port = port
		}
		if err := checkTunnelID(c); err != nil {
			return Change{}, err
		}
	}
	lc := *t
	lc.Lanes = lanesOf(set.Lanes)
	if err := checkLanes(lc); err != nil {
		return Change{}, err
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
		top.MTU, top.Profile = set.MTU, profileOf(set.Profile)
		top.Lanes = lanesOf(set.Lanes)
		b.MTUProbe, b.TTL, b.Encrypt = set.MTUProbe, set.TTL, set.Encrypt
		b.NoDF, b.DSCP = set.NoDF, set.DSCP
		b.Keepalive, b.Padding = set.Keepalive, set.Padding
		if b.Kind == config.KindWireGuard {
			b.PeerKey = set.PeerKey
		}
		if top.Kind == config.KindEoIP {
			top.TunnelID = set.TunnelID
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
		Padding: b.Padding, Keepalive: b.Keepalive, PeerKey: b.PeerKey, TunnelID: top.TunnelID, Profile: top.Profile, Lanes: top.Lanes}, b.Port
}

// TunnelOpts describes a single tunnel of any kind.
type TunnelOpts struct {
	Kind, Peer, Remote, Over string
	MTU                      int
	Port                     uint16
	VNI                      uint32
	Settings
	// stacked: the tunnel will run over another one of the same request, on
	// its inner addresses, so it cannot clash with tunnels on public ones.
	stacked bool
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
	if kind == config.KindEoIP {
		t.TunnelID = o.TunnelID
	} else if o.TunnelID != nil {
		return t, fmt.Errorf("a tunnel id is only for EoIP")
	}
	if o.Port != 0 {
		if !ki.CanUsePort() {
			return t, fmt.Errorf("%s cannot run on a UDP port", kind)
		}
		if err := portAvailable(st, o.Port, ""); err != nil {
			return t, err
		}
	} else if (ki.UDP || ki.TCP) && kind != config.KindVXLAN && kind != config.KindGENEVE && o.Over == "" {
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
	if err := checkTunnelID(t); err != nil {
		return t, err
	}
	t.Lanes = lanesOf(o.Lanes)
	if err := checkLanes(t); err != nil {
		return t, err
	}
	if other := keylessConflict(st, t); other != "" && !o.stacked {
		return t, fmt.Errorf("tunnel %s already runs %s between these two servers and the kernel allows only one, even on a UDP port: pick GRE, GENEVE or another kind", other, kind)
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

// checkTunnelID rejects an EoIP tunnel id too large for raw EoIPv6's 12
// bits, judging the underlay by the far end's address. Over another tunnel
// the family is known only once subnets are allocated, so rendering checks.
func checkTunnelID(t config.Tunnel) error {
	if t.TunnelID == nil || t.Over != "" {
		return nil
	}
	a, err := netip.ParseAddr(t.Sides[1].Endpoint)
	if err != nil {
		return nil
	}
	if limit := config.EoIPMaxID(a.Unmap().Is6(), t.Port != 0); *t.TunnelID > limit {
		return fmt.Errorf("EoIPv6 without a port carries a 12-bit tunnel id: choose 0-%d", limit)
	}
	return nil
}

// keylessConflict reports a tunnel the kernel can't tell apart from t: SIT,
// IPIP and ip6tnl tunnels have no key, so only one of each type may run
// between the same two addresses, whether or not it runs on a UDP port.
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
	if g == "" || t.Over != "" {
		return ""
	}
	ts, _ := st.Effective()
	for _, o := range ts {
		if o.Name != t.Name && o.Over == "" && group(o.Kind) == g &&
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

// FreePort picks an unused UDP (or TCP) port for a kind that always needs one.
func FreePort(st *config.State, kind config.Kind) uint16 {
	start := uint16(51820)
	switch kind {
	case config.KindObfs:
		start = 30443
	case config.KindTCP:
		start = 31443
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
			if drop[f.Backup] {
				f.Backup = ""
			}
			fs = append(fs, f)
		}
	}
	s.Forwards = fs
}

// Limits are a tunnel's switch and caps, set on the user-facing tunnel.
type Limits struct {
	Disabled bool    `json:"disabled"`
	RateMbit int     `json:"rate_mbit"`
	QuotaGB  float64 `json:"quota_gb"`
}

// SetLimits turns a tunnel on or off and sets its speed limit and monthly
// quota. Both ends apply them.
func SetLimits(name string, l Limits) (Change, error) {
	if l.RateMbit < 0 || l.RateMbit > 100_000 {
		return Change{}, fmt.Errorf("speed limit must be 0 (none) to 100000 Mbit/s")
	}
	if l.QuotaGB < 0 || l.QuotaGB > 1_000_000 {
		return Change{}, fmt.Errorf("quota must be 0 (none) to 1000000 GB")
	}
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return Change{}, fmt.Errorf("unknown tunnel %q", name)
	}
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error {
		top, ok := s.Tunnel(name)
		if !ok {
			return fmt.Errorf("tunnel %q changed meanwhile", name)
		}
		top.Disabled, top.RateMbit, top.QuotaGB = l.Disabled, l.RateMbit, l.QuotaGB
		return nil
	})
	return ch, err
}

// LimitsOf returns a tunnel's switch and caps.
func LimitsOf(t config.Tunnel) Limits {
	return Limits{Disabled: t.Disabled, RateMbit: t.RateMbit, QuotaGB: t.QuotaGB}
}

// profileOf stores balanced as "".
func profileOf(p string) string {
	if p == "balanced" {
		return ""
	}
	return p
}

// lanesOf stores one lane as 0.
func lanesOf(n int) int {
	if n <= 1 {
		return 0
	}
	return n
}

// checkLanes allows several lanes only where they work: kernel GRE kinds
// (each lane is a GRE key) to a far end that runs NeoTunnel.
func checkLanes(t config.Tunnel) error {
	if t.LaneCount() < 2 {
		return nil
	}
	if !config.LaneKinds(t.Kind) {
		return fmt.Errorf("%s cannot run on several lanes; GRE and GRETAP can", t.Kind)
	}
	if t.Sides[0].Node == "" || t.Sides[1].Node == "" {
		return fmt.Errorf("lanes need NeoTunnel on the far end (toward a MikroTik, bond several EoIP tunnels there)")
	}
	return nil
}

// RandomPort picks a random port between 20000 and 60000 for a tunnel
// that no tunnel, forward, the panel or a reserved service uses here, and
// that nothing else on this server is bound to (tcp or udp). The far
// server's side is checked by the wizard's live test.
func RandomPort(st *config.State, proto string) (uint16, error) {
	if proto != "tcp" {
		proto = "udp"
	}
	for range 300 {
		p := uint16(20000 + rand.IntN(40001))
		if portAvailable(st, p, "") != nil || portsFree(st, strconv.Itoa(int(p)), proto, "") != nil {
			continue
		}
		if !bindable(proto, p) {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free %s port found between 20000 and 60000", proto)
}

// bindable reports whether nothing on this server holds proto/port.
func bindable(proto string, port uint16) bool {
	addr := ":" + strconv.Itoa(int(port))
	if proto == "tcp" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		l.Close()
		return true
	}
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
