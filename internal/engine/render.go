// Package engine turns tunnel definitions into kernel interfaces and keeps
// them in sync.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"

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
	Padding   int    `json:"padding,omitempty"`
	Keepalive int    `json:"keepalive,omitempty"`
	PeerKey   string `json:"peer_key,omitempty"`
	Userspace bool   `json:"userspace,omitempty"`
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

// Resolver returns the local source address used to reach remote.
type Resolver func(remote netip.Addr) (netip.Addr, error)

// Render resolves every tunnel this node runs into link specs, parents first.
// Tunnels that fail to render are reported in errs and skipped, so one bad
// definition never takes the rest down.
func Render(st *config.State, resolve Resolver) (specs []LinkSpec, errs []error) {
	ts, _ := st.Effective()
	return RenderTunnels(st.IsSelf, ts, resolve)
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
		return s, nil
	}
	for _, t := range ts {
		if _, err := visit(t.Name, 0); err != nil {
			errs = append(errs, err)
		}
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
	if t.Encrypt != "" {
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
	}
	if ki.PortEncap {
		s.Port = t.Port
	}
	if ki.Encrypted && t.Secret == "" {
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
	return s, nil
}
