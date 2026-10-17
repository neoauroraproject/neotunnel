// Package config holds NeoTunnel's persistent data model.
//
// A node keeps two sets of definitions: Local (owned and edited on this node,
// including the tunnels it manages on its peers) and Managed (pushed by the
// node's master and only replaced as a whole). Tunnels are described once,
// symmetrically, by their two Sides; each node renders its own end.
package config

import (
	"fmt"
	"regexp"
	"time"
)

const SchemaVersion = 1

type State struct {
	Schema   int    `json:"schema"`
	Self     string `json:"self"`     // this node's name
	Revision uint64 `json:"revision"` // bumped on every local change
	ULA48    string `json:"ula48,omitempty"`
	Address  string `json:"address,omitempty"` // this node's public IP, used in setup links

	Tunnels  []Tunnel  `json:"tunnels,omitempty"`
	Forwards []Forward `json:"forwards,omitempty"`
	Peers    []Peer    `json:"peers,omitempty"`

	Managed  *Managed  `json:"managed,omitempty"`
	Panel    *Panel    `json:"panel,omitempty"`
	Defaults *Defaults `json:"defaults,omitempty"`
}

// Defaults pre-fill the advanced options of new tunnels and forwards.
type Defaults struct {
	Encrypt     string `json:"encrypt,omitempty"`      // IPsec cipher for new tunnels to NeoTunnel servers
	MTUProbe    bool   `json:"mtu_probe,omitempty"`    // measure the path MTU of new tunnels
	Obfs        bool   `json:"obfs,omitempty"`         // run new tunnels over a NeoObfs layer
	Padding     int    `json:"padding,omitempty"`      // NeoObfs random padding
	ForwardMode string `json:"forward_mode,omitempty"` // nat, transparent, proxy
}

// D returns the defaults, never nil.
func (s *State) D() Defaults {
	if s.Defaults == nil {
		return Defaults{}
	}
	return *s.Defaults
}

// Panel is the web panel's configuration (nil = panel disabled).
type Panel struct {
	Port     int    `json:"port"`
	Path     string `json:"path"` // secret URL prefix, e.g. "/k3x9q2"
	User     string `json:"user"`
	PassHash string `json:"pass_hash"` // bcrypt
}

// Managed is the part of the state owned by this node's master.
type Managed struct {
	Master   MasterRef `json:"master"`
	Self     string    `json:"self"` // the name the master knows us by
	Revision uint64    `json:"revision"`
	Tunnels  []Tunnel  `json:"tunnels,omitempty"`
	Forwards []Forward `json:"forwards,omitempty"`

	// EnrollToken lets the daemon register with the master once it becomes
	// reachable. Cleared after a successful registration.
	EnrollToken string `json:"enroll_token,omitempty"`
}

type MasterRef struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"` // base64 ed25519
	Address   string `json:"address"`    // host:port of the master's agent
}

type Peer struct {
	Name      string `json:"name"`
	Address   string `json:"address"` // public IP/host the peer is reached at
	AgentPort int    `json:"agent_port"`
	PublicKey string `json:"public_key,omitempty"` // empty until paired

	// EnrollToken authorizes the peer's first registration and the binary
	// download; it is reused while valid so earlier links keep working.
	EnrollToken   string    `json:"enroll_token,omitempty"`
	EnrollExpires time.Time `json:"enroll_expires,omitempty"`

	Version string `json:"version,omitempty"`
	Arch    string `json:"arch,omitempty"`
	// Address6 is the peer's public IPv6, as it reports it, for tunnels over
	// an IPv6 underlay.
	Address6        string    `json:"address6,omitempty"`
	AppliedRevision uint64    `json:"applied_revision,omitempty"`
	LastSeen        time.Time `json:"last_seen,omitempty"`
}

func (p Peer) Paired() bool { return p.PublicKey != "" }

type Tunnel struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Over names another tunnel whose inner addresses serve as this tunnel's
	// underlay (e.g. ipip6 over sit for the classic "6to4" stack).
	Over  string  `json:"over,omitempty"`
	Sides [2]Side `json:"sides"`

	Net4 string `json:"net4,omitempty"` // inner IPv4 subnet, side 0 = .1, side 1 = .2
	Net6 string `json:"net6,omitempty"` // inner IPv6 subnet, side 0 = ::1, side 1 = ::2

	Key uint32 `json:"key,omitempty"` // GRE key
	VNI uint32 `json:"vni,omitempty"` // VXLAN / GENEVE id
	// Port is the UDP port the tunnel runs on: VXLAN/GENEVE always use one;
	// SIT, IPIP, GRE and GRETAP are wrapped in UDP (FOU) when it is set.
	Port uint16 `json:"port,omitempty"`
	MTU  int    `json:"mtu,omitempty"` // 0 = automatic
	TTL  uint8  `json:"ttl,omitempty"` // 0 = 255
	// MTUProbe measures the path MTU to the far end and sizes the tunnel
	// from it (re-checked periodically) instead of assuming 1500.
	MTUProbe bool `json:"mtu_probe,omitempty"`
	// NoDF clears the Don't-Fragment bit on the outer packets, so an
	// oversized packet is fragmented on the path instead of dropped. Default
	// (false) sets DF, which is faster but needs a correct MTU.
	NoDF bool `json:"no_df,omitempty"`
	// DSCP is the outer packets' DSCP class: 0 inherits from the inner
	// packet, 1-63 sets a fixed class (e.g. for a provider that prioritizes
	// one). Only kernel IP tunnels honor it.
	DSCP uint8 `json:"dscp,omitempty"`

	// Secret seeds every key of the tunnel (IPsec, WireGuard, NeoObfs):
	// 32 random bytes, base64, shared by both sides.
	Secret string `json:"secret,omitempty"`
	// Encrypt protects the tunnel's outer packets with kernel IPsec (ESP in
	// transport mode, like MikroTik's ipsec-secret); see Ciphers.
	Encrypt string `json:"encrypt,omitempty"`
	// Padding adds up to this many random bytes to NeoObfs packets so their
	// sizes don't reveal the traffic inside.
	Padding int `json:"padding,omitempty"`
	// Keepalive in seconds for WireGuard and NeoObfs (0 = 25, -1 = off).
	Keepalive int `json:"keepalive,omitempty"`
	// PeerKey is a WireGuard far end's public key when it doesn't run
	// NeoTunnel (e.g. a MikroTik); managed ends derive keys from Secret.
	PeerKey string `json:"peer_key,omitempty"`

	// Mode labels the user-facing tunnel of a stack (e.g. "6to4").
	Mode string `json:"mode,omitempty"`

	// Disabled keeps the tunnel defined but down, with every tunnel under it
	// that nothing else uses.
	Disabled bool `json:"disabled,omitempty"`
	// RateMbit caps what each side sends into the tunnel (0 = no limit), so
	// both directions are limited.
	RateMbit int `json:"rate_mbit,omitempty"`
	// QuotaGB is a monthly traffic allowance (download + upload): once a
	// side has counted this much this month, it takes the tunnel down until
	// the month ends or the quota is raised.
	QuotaGB float64 `json:"quota_gb,omitempty"`
}

type Side struct {
	Node     string `json:"node"`
	Endpoint string `json:"endpoint,omitempty"` // public address the other side dials
	Local    string `json:"local,omitempty"`    // optional source override (NAT)
}

// SideOf returns this node's side index and whether the tunnel involves it.
func (t Tunnel) SideOf(node string) (int, bool) {
	switch node {
	case t.Sides[0].Node:
		return 0, true
	case t.Sides[1].Node:
		return 1, true
	}
	return 0, false
}

// Forward exposes ports on Node and DNATs them through Tunnel to the far end.
// Different forwards can send different ports to different tunnels, so one
// server can serve many destinations.
type Forward struct {
	Node   string `json:"node"`
	Tunnel string `json:"tunnel"`
	Proto  string `json:"proto"`        // tcp, udp, both
	Ports  string `json:"ports"`        // "443,8080-8090"
	To     uint16 `json:"to,omitempty"` // target port on the far end; 0 = same port
	// Mode decides what the far end sees as the client's address:
	//   nat (default)  this node's tunnel address (kernel DNAT + masquerade)
	//   transparent    the real client IP (kernel; the far end routes replies
	//                  back through the tunnel, so it must run NeoTunnel)
	//   proxy          a PROXY protocol header carrying the client's address,
	//                  sent by a relay in the daemon (nginx, HAProxy, Xray...)
	Mode string `json:"mode,omitempty"`
	// ProxyVersion is the PROXY protocol version: 2 (default, TCP and UDP)
	// or 1 (text, TCP only).
	ProxyVersion int `json:"proxy_version,omitempty"`
}

// Forward modes.
const (
	ForwardNAT         = "nat"
	ForwardTransparent = "transparent"
	ForwardProxy       = "proxy"
)

// ModeOf returns the forward's mode with the default filled in.
func (f Forward) ModeOf() string {
	if f.Mode == "" {
		return ForwardNAT
	}
	return f.Mode
}

// ValidForwardMode checks a mode and PROXY protocol version for proto.
func ValidForwardMode(mode string, version int, proto string) error {
	switch mode {
	case "", ForwardNAT, ForwardTransparent:
	case ForwardProxy:
		if version != 0 && version != 1 && version != 2 {
			return fmt.Errorf("PROXY protocol version must be 1 or 2")
		}
		if version == 1 && proto != "tcp" {
			return fmt.Errorf("PROXY protocol v1 only works with TCP; use v2 for UDP")
		}
	default:
		return fmt.Errorf("bad forward mode %q (nat, transparent, proxy)", mode)
	}
	return nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,9}$`)

// ValidName enforces names that fit Linux interface names as "nt-<name>".
func ValidName(s string) error {
	if !nameRe.MatchString(s) {
		return fmt.Errorf("invalid name %q: use 1-10 chars of a-z, 0-9, '-'", s)
	}
	return nil
}

func (s *State) Tunnel(name string) (*Tunnel, bool) {
	for i := range s.Tunnels {
		if s.Tunnels[i].Name == name {
			return &s.Tunnels[i], true
		}
	}
	return nil, false
}

func (s *State) Peer(name string) (*Peer, bool) {
	for i := range s.Peers {
		if s.Peers[i].Name == name {
			return &s.Peers[i], true
		}
	}
	return nil, false
}

// ForPeer builds the managed state a peer should run: every local tunnel and
// forward that involves it.
func (s *State) ForPeer(peer string) ([]Tunnel, []Forward) {
	var ts []Tunnel
	for _, t := range s.Tunnels {
		if _, ok := t.SideOf(peer); ok {
			ts = append(ts, t)
		}
	}
	touches := map[string]bool{}
	for _, t := range ts {
		touches[t.Name] = true
	}
	var fs []Forward
	for _, f := range s.Forwards {
		// The far end of a transparent forward must know about it to route
		// replies back through the tunnel.
		if f.Node == peer || (f.ModeOf() == ForwardTransparent && touches[f.Tunnel]) {
			fs = append(fs, f)
		}
	}
	return ts, fs
}

// PeersOf lists the peers a tunnel touches (the side that isn't Self).
func (s *State) PeersOf(t Tunnel) []string {
	var out []string
	for _, sd := range t.Sides {
		if sd.Node != s.Self {
			if _, ok := s.Peer(sd.Node); ok {
				out = append(out, sd.Node)
			}
		}
	}
	return out
}

// Effective returns all tunnels and forwards this node must run, local and
// managed. Use IsSelf to find this node's side of each tunnel.
func (s *State) Effective() (ts []Tunnel, fs []Forward) {
	for _, t := range s.Tunnels {
		if _, ok := t.SideOf(s.Self); ok {
			ts = append(ts, t)
		}
	}
	for _, f := range s.Forwards {
		if f.Node == s.Self {
			fs = append(fs, f)
		}
	}
	if m := s.Managed; m != nil {
		for _, t := range m.Tunnels {
			if _, ok := t.SideOf(m.Self); ok {
				ts = append(ts, t)
			}
		}
		for _, f := range m.Forwards {
			if f.Node == m.Self {
				fs = append(fs, f)
			}
		}
	}
	return ts, fs
}

// ReturnPaths lists this node's tunnels that carry transparent forwards from
// the other side: replies to those clients must go back through the tunnel.
func (s *State) ReturnPaths() []string {
	fs := append([]Forward(nil), s.Forwards...)
	if s.Managed != nil {
		fs = append(fs, s.Managed.Forwards...)
	}
	ts, _ := s.Effective()
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		if f.ModeOf() != ForwardTransparent || s.IsSelf(f.Node) || seen[f.Tunnel] {
			continue
		}
		for _, t := range ts {
			if t.Name == f.Tunnel {
				seen[f.Tunnel] = true
				out = append(out, f.Tunnel)
			}
		}
	}
	return out
}

// IsSelf reports whether a node name in a definition refers to this node,
// either by our own name or by the name our master gave us.
func (s *State) IsSelf(node string) bool {
	return node == s.Self || (s.Managed != nil && node == s.Managed.Self)
}
