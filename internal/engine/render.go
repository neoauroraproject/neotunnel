// Package engine turns tunnel definitions into kernel interfaces and keeps
// them in sync.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sync"

	"neotunnel/internal/config"
	"neotunnel/internal/pmtu"
)

const (
	IfPrefix      = "nt-"
	DefaultMTU    = 1500
	DefaultTTL    = 255
	DefaultPort   = 4789 // VXLAN
	GenevePort    = 6081
	WireGuardPort = 51820
)

// LinkSpec is one interface this node must have, fully resolved.
type LinkSpec struct {
	Name   string         `json:"name"`
	Tunnel string         `json:"tunnel"`
	Kind   config.Kind    `json:"kind"`
	Local  netip.Addr     `json:"local"`
	Remote netip.Addr     `json:"remote"`
	Key    uint32         `json:"key,omitempty"`
	VNI    uint32         `json:"vni,omitempty"`
	Port   uint16         `json:"port,omitempty"`
	FOU    bool           `json:"fou,omitempty"` // Port is a FOU (UDP) encapsulation port
	TTL    uint8          `json:"ttl"`
	MTU    int            `json:"mtu"`
	NoDF   bool           `json:"no_df,omitempty"`
	DSCP   uint8          `json:"dscp,omitempty"`
	Addrs  []netip.Prefix `json:"addrs"`
	Parent string         `json:"parent,omitempty"`

	// Side is this node's side of the tunnel (0 or 1); keys are derived
	// per direction from Secret, which never leaves the node in a spec.
	Side      int    `json:"side"`
	Secret    string `json:"-"`
	Encrypt   string `json:"encrypt,omitempty"` // IPsec cipher on the outer packets
	Cipher    string `json:"cipher,omitempty"`  // user-space stream cipher (TCP)
	Padding   int    `json:"padding,omitempty"`
	Keepalive int    `json:"keepalive,omitempty"`
	PeerKey   string `json:"peer_key,omitempty"`
	Userspace bool   `json:"userspace,omitempty"`
	// RateMbit shapes what this side sends (0 = no limit). It is applied
	// in place, so it is not part of Hash.
	RateMbit int `json:"rate_mbit,omitempty"`
	// Profile is the stack's speed profile (see config.ProfileMax); applied
	// in place, so not part of Hash.
	Profile string `json:"profile,omitempty"`
	// Lane is this interface's lane of a tunnel on several lanes (0 = the
	// tunnel's own interface, named like any other) and Lanes how many the
	// tunnel has. Every lane holds this end's inner addresses as host
	// addresses; Far are the other end's, reached by one multipath route
	// over the lanes.
	Lane  int          `json:"lane,omitempty"`
	Lanes int          `json:"lanes,omitempty"`
	Far   []netip.Addr `json:"far,omitempty"`
}

// Hash identifies the spec's kernel-relevant settings; it is stored in the
// interface alias so unchanged tunnels are never torn down.
func (l LinkSpec) Hash() string {
	// WireGuard's keys are set at creation only, so they are part of the
	// identity (as a digest: the alias is world readable).
	var keys string
	if l.Kind == config.KindWireGuard {
		d := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", l.Secret, l.PeerKey, l.Side, l.Keepalive)))
		keys = hex.EncodeToString(d[:6])
	}
	b, _ := json.Marshal(struct {
		K         config.Kind
		L, R      netip.Addr
		Key, VNI  uint32
		Port      uint16
		FOU, NoDF bool
		TTL, DSCP uint8
		Keys      string `json:",omitempty"`
	}{l.Kind, l.Local, l.Remote, l.Key, l.VNI, l.Port, l.FOU, l.NoDF, l.TTL, l.DSCP, keys})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// Ifname is the interface name for a tunnel.
func Ifname(tunnel string) string { return IfPrefix + tunnel }

// LaneName is the interface of a tunnel's lane: lane 0 is the tunnel's own
// interface, the others are nt-<name>.<n> ('.' never appears in names).
func LaneName(tunnel string, lane int) string {
	if lane == 0 {
		return Ifname(tunnel)
	}
	return fmt.Sprintf("%s.%d", Ifname(tunnel), lane)
}

// LaneNames lists every interface of a tunnel.
func LaneNames(t config.Tunnel) []string {
	out := make([]string, t.LaneCount())
	for i := range out {
		out[i] = LaneName(t.Name, i)
	}
	return out
}

// Resolver returns the local source address used to reach remote.
type Resolver func(remote netip.Addr) (netip.Addr, error)

// Render resolves every tunnel this node runs into link specs, parents first.
// Tunnels that fail to render are reported in errs and skipped, so one bad
// definition never takes the rest down.
func Render(st *config.State, resolve Resolver) (specs []LinkSpec, errs []error) {
	ts, _ := st.Effective()
	return RenderTunnels(st.IsSelf, Active(ts), resolve)
}

var (
	blockedMu sync.Mutex
	blocked   = map[string]bool{}
)

// SetBlocked sets the tunnels that are down for having used their quota.
func SetBlocked(names map[string]bool) {
	blockedMu.Lock()
	defer blockedMu.Unlock()
	blocked = names
}

// Blocked reports whether a tunnel is down for having used its quota.
func Blocked(name string) bool {
	blockedMu.Lock()
	defer blockedMu.Unlock()
	return blocked[name]
}

// Active drops disabled and over-quota tunnels, and the tunnels under them
// that no running tunnel still needs.
func Active(ts []config.Tunnel) []config.Tunnel {
	off := map[string]bool{}
	users := map[string][]string{}
	for _, t := range ts {
		if t.Disabled || Blocked(t.Name) {
			off[t.Name] = true
		}
		if t.Over != "" {
			users[t.Over] = append(users[t.Over], t.Name)
		}
	}
	// A carrier goes when every tunnel over it went; repeat for deep stacks.
	for changed := true; changed; {
		changed = false
		for _, t := range ts {
			us := users[t.Name]
			if off[t.Name] || len(us) == 0 {
				continue
			}
			all := true
			for _, u := range us {
				all = all && off[u]
			}
			if all {
				off[t.Name], changed = true, true
			}
		}
	}
	if len(off) == 0 {
		return ts
	}
	out := make([]config.Tunnel, 0, len(ts))
	for _, t := range ts {
		if !off[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

func RenderTunnels(isSelf func(string) bool, ts []config.Tunnel, resolve Resolver) (specs []LinkSpec, errs []error) {
	byName := map[string]config.Tunnel{}
	for _, t := range ts {
		if _, dup := byName[t.Name]; dup {
			errs = append(errs, fmt.Errorf("tunnel %s: defined twice (local and managed?)", t.Name))
			continue
		}
		byName[t.Name] = t
	}
	done := map[string]*LinkSpec{}
	var visit func(name string, depth int) (*LinkSpec, error)
	visit = func(name string, depth int) (*LinkSpec, error) {
		if s, ok := done[name]; ok {
			return s, nil
		}
		if depth > 4 {
			return nil, fmt.Errorf("tunnel %s: 'over' chain too deep or circular", name)
		}
		t, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("tunnel %s: not found", name)
		}
		var parent *LinkSpec
		if t.Over != "" {
			p, err := visit(t.Over, depth+1)
			if err != nil {
				return nil, fmt.Errorf("tunnel %s: over %s: %w", name, t.Over, err)
			}
			parent = p
		}
		s, err := renderOne(isSelf, t, byName, parent, resolve)
		if err != nil {
			return nil, fmt.Errorf("tunnel %s: %w", name, err)
		}
		done[name] = s
		specs = append(specs, *s)
		specs = append(specs, laneSpecs(*s)...)
		return s, nil
	}
	for _, t := range ts {
		if _, err := visit(t.Name, 0); err != nil {
			errs = append(errs, err)
		}
	}
	// A stack's profile is set on its top tunnel; every layer under it
	// follows it (the fastest one wins where layers are shared).
	profile := map[string]string{}
	for _, t := range ts {
		if t.Profile == "" {
			continue
		}
		for n, depth := t.Name, 0; n != "" && depth < 6; depth++ {
			if profile[n] != config.ProfileMax {
				profile[n] = t.Profile
			}
			n = byName[n].Over
		}
	}
	for i := range specs {
		specs[i].Profile = profile[specs[i].Tunnel]
	}
	return specs, errs
}

func sideIndex(isSelf func(string) bool, t config.Tunnel) (int, error) {
	a, b := isSelf(t.Sides[0].Node), isSelf(t.Sides[1].Node)
	switch {
	case a && b:
		return 0, fmt.Errorf("both sides are this node")
	case a:
		return 0, nil
	case b:
		return 1, nil
	}
	return 0, fmt.Errorf("does not involve this node")
}

func renderOne(isSelf func(string) bool, t config.Tunnel, byName map[string]config.Tunnel, parent *LinkSpec, resolve Resolver) (*LinkSpec, error) {
	ki, err := config.LookupKind(t.Kind)
	if err != nil {
		return nil, err
	}
	me, err := sideIndex(isSelf, t)
	if err != nil {
		return nil, err
	}
	other := 1 - me

	var local, remote netip.Addr
	underMTU := DefaultMTU
	if parent != nil {
		pt := byName[t.Over]
		net := pt.Net4
		if ki.Under6 || (ki.UnderAny && pt.Net4 == "") {
			net = pt.Net6
		}
		l, ok1 := config.Inner(net, me)
		r, ok2 := config.Inner(net, other)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("parent %s has no inner %s subnet", t.Over, map[bool]string{true: "IPv6", false: "IPv4"}[ki.Under6])
		}
		local, remote = l.Addr(), r.Addr()
		underMTU = parent.MTU
	} else {
		remote, err = netip.ParseAddr(t.Sides[other].Endpoint)
		if err != nil {
			return nil, fmt.Errorf("side %s: endpoint %q is not an IP address", t.Sides[other].Node, t.Sides[other].Endpoint)
		}
		if lo := t.Sides[me].Local; lo != "" {
			if local, err = netip.ParseAddr(lo); err != nil {
				return nil, fmt.Errorf("local override %q: %w", lo, err)
			}
		} else if local, err = resolve(remote); err != nil {
			return nil, fmt.Errorf("no route to %s: %w", remote, err)
		}
		if t.MTUProbe {
			if m := pmtu.Cached(remote.Unmap()); m > 0 {
				underMTU = m
			}
		}
	}
	remote, local = remote.Unmap(), local.Unmap()
	under6 := remote.Is6()
	if !ki.UnderAny && ki.Under6 != under6 {
		want := map[bool]string{true: "IPv6", false: "IPv4"}[ki.Under6]
		return nil, fmt.Errorf("%s needs an %s underlay, got %s", t.Kind, want, remote)
	}
	if local.Is6() != under6 {
		return nil, fmt.Errorf("local %s and remote %s are different families", local, remote)
	}

	s := &LinkSpec{
		Name:      Ifname(t.Name),
		Tunnel:    t.Name,
		Kind:      t.Kind,
		Local:     local,
		Remote:    remote,
		TTL:       t.TTL,
		NoDF:      t.NoDF,
		DSCP:      t.DSCP,
		MTU:       t.MTU,
		Side:      me,
		Secret:    t.Secret,
		Padding:   t.Padding,
		Keepalive: t.Keepalive,
		PeerKey:   t.PeerKey,
		Userspace: ki.Userspace,
		RateMbit:  t.RateMbit,
	}
	if parent != nil {
		s.Parent = parent.Name
	}
	if s.TTL == 0 {
		s.TTL = DefaultTTL
	}
	switch {
	case t.Port != 0 && ki.FOU:
		if under6 {
			return nil, fmt.Errorf("a UDP port (FOU) needs an IPv4 underlay")
		}
		s.Port, s.FOU = t.Port, true
	case t.Port != 0 && !ki.CanUsePort():
		return nil, fmt.Errorf("%s cannot run on a port", t.Kind)
	}
	overhead := ki.OverheadFor(under6, t.Port != 0)
	// Routers like MikroTik send GRE without a key, and Linux only matches
	// keyless packets to a keyless tunnel: toward a far end that doesn't run
	// NeoTunnel, GRE goes without one.
	keyless := ki.IsGRE() && t.Sides[other].Node == ""
	if keyless {
		overhead -= 4
	}
	if t.Kind == config.KindObfs {
		overhead += t.Padding
	}
	if t.Kind == config.KindTCP && t.Encrypt != "" {
		// The TCP transport seals its own frames; no IPsec underneath.
		if _, err := config.LookupCipher(t.Encrypt); err != nil {
			return nil, err
		}
		s.Cipher = t.Encrypt
		overhead += config.TCPTagOverhead
	} else if t.Encrypt != "" {
		if ki.Encrypted {
			return nil, fmt.Errorf("%s is already encrypted; IPsec is not needed", t.Kind)
		}
		if parent != nil {
			return nil, fmt.Errorf("encryption belongs on the bottom tunnel of a stack")
		}
		if _, err := config.LookupCipher(t.Encrypt); err != nil {
			return nil, err
		}
		s.Encrypt = t.Encrypt
		overhead += config.ESPOverhead
	}
	if s.MTU == 0 {
		s.MTU = underMTU - overhead
	}
	if s.MTU < 1280 && ki.Carry6 && t.MTU == 0 {
		// IPv6 needs 1280; better a fragmenting underlay than a dead tunnel.
		s.MTU = 1280
	}
	switch t.Kind {
	case config.KindGRE, config.KindGRETAP, config.KindIP6GRE, config.KindIP6GRETAP, config.KindL2TPv3:
		if !keyless {
			s.Key = t.Key
		}
	case config.KindVXLAN, config.KindGENEVE:
		s.VNI, s.Port = t.VNI, t.Port
		if s.Port == 0 {
			s.Port = map[config.Kind]uint16{config.KindVXLAN: DefaultPort, config.KindGENEVE: GenevePort}[t.Kind]
		}
	case config.KindWireGuard:
		s.Port = t.Port
		if s.Port == 0 {
			s.Port = WireGuardPort
		}
	case config.KindObfs:
		if t.Port == 0 {
			return nil, fmt.Errorf("NeoObfs needs a UDP port")
		}
		s.Port = t.Port
	case config.KindTCP:
		if t.Port == 0 {
			return nil, fmt.Errorf("a TCP tunnel needs a TCP port")
		}
		s.Port = t.Port
	case config.KindEoIP:
		// The tunnel id is 16 bits in EoIP's GRE header and 12 in EoIPv6's.
		limit := config.EoIPMaxID(under6, t.Port != 0)
		if t.TunnelID != nil {
			if *t.TunnelID > limit {
				return nil, fmt.Errorf("EoIPv6 tunnel id must be 0-%d, got %d", limit, *t.TunnelID)
			}
			s.Key = uint32(*t.TunnelID)
		} else {
			s.Key = t.Key & uint32(limit)
		}
	}
	if ki.PortEncap {
		s.Port = t.Port
	}
	if (ki.Encrypted || ki.NeedsPeer) && t.Secret == "" {
		return nil, fmt.Errorf("no secret: re-create the tunnel")
	}
	for _, n := range []string{t.Net4, t.Net6} {
		if n == "" {
			continue
		}
		p, ok := config.Inner(n, me)
		if !ok {
			return nil, fmt.Errorf("bad inner subnet %q", n)
		}
		s.Addrs = append(s.Addrs, p)
	}
	if n := t.LaneCount(); n > 1 {
		if err := setLanes(s, t, n, keyless); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// setLanes turns s into lane 0 of a tunnel on n lanes: its addresses become
// host addresses (the same on every lane) and Far the other end's, which
// the engine routes over all lanes at once.
func setLanes(s *LinkSpec, t config.Tunnel, n int, keyless bool) error {
	switch {
	case n > config.MaxLanes:
		return fmt.Errorf("at most %d lanes", config.MaxLanes)
	case !config.LaneKinds(t.Kind):
		return fmt.Errorf("%s cannot run on several lanes (GRE and GRETAP can)", t.Kind)
	case keyless:
		return fmt.Errorf("lanes need NeoTunnel on the far end (each lane is a GRE key)")
	}
	s.Lanes = n
	if s.RateMbit > 0 {
		// Each lane shapes its own share of the cap.
		s.RateMbit = (s.RateMbit + n - 1) / n
	}
	for i, p := range s.Addrs {
		far, _ := config.Inner(p.Masked().String(), 1-s.Side)
		s.Far = append(s.Far, far.Addr())
		s.Addrs[i] = netip.PrefixFrom(p.Addr(), p.Addr().BitLen())
	}
	return nil
}

// laneSpecs are the other lanes of a tunnel on several lanes: copies of lane
// 0 with their own interface and GRE key (key+1, key+2, ...).
func laneSpecs(s LinkSpec) []LinkSpec {
	var out []LinkSpec
	for i := 1; i < s.Lanes; i++ {
		l := s
		l.Name, l.Lane = LaneName(s.Tunnel, i), i
		l.Key = s.Key + uint32(i)
		if l.Key < uint32(i) { // wrapped past zero, which would mean no key
			l.Key++
		}
		l.Addrs = append([]netip.Prefix(nil), s.Addrs...)
		l.Far = append([]netip.Addr(nil), s.Far...)
		out = append(out, l)
	}
	return out
}

// activeForwards drops the forwards of tunnels that are turned off, which
// are down on purpose rather than broken.
func activeForwards(ts []config.Tunnel, fs []config.Forward) []config.Forward {
	on := map[string]bool{}
	for _, t := range Active(ts) {
		on[t.Name] = true
	}
	out := fs[:0:0]
	for _, f := range fs {
		known := false
		for _, t := range ts {
			known = known || t.Name == f.Tunnel
		}
		if on[f.Tunnel] || !known {
			out = append(out, f)
		}
	}
	return out
}

// TunnelState is a tunnel's live state with the counters of all its lanes
// added up (lane 0's state and MTU otherwise).
func TunnelState(tunnel string) LinkState {
	s := State(Ifname(tunnel))
	for i := 1; i < config.MaxLanes && s.Exists; i++ {
		l := State(LaneName(tunnel, i))
		if !l.Exists {
			break
		}
		s.RxBytes, s.TxBytes = s.RxBytes+l.RxBytes, s.TxBytes+l.TxBytes
		s.RxPkts, s.TxPkts = s.RxPkts+l.RxPkts, s.TxPkts+l.TxPkts
	}
	return s
}
