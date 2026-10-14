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

	Managed *Managed `json:"managed,omitempty"`
	Panel   *Panel   `json:"panel,omitempty"`
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

	Version         string    `json:"version,omitempty"`
	Arch            string    `json:"arch,omitempty"`
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

	// Mode labels the user-facing tunnel of a stack (e.g. "6to4").
	Mode string `json:"mode,omitempty"`
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
	var fs []Forward
	for _, f := range s.Forwards {
		if f.Node == peer {
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

// IsSelf reports whether a node name in a definition refers to this node,
// either by our own name or by the name our master gave us.
func (s *State) IsSelf(node string) bool {
	return node == s.Self || (s.Managed != nil && node == s.Managed.Self)
}
