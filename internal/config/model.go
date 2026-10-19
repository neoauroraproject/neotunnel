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

	// Managed is the first server that manages this node, More the others
	// (see Masters).
	Managed  *Managed   `json:"managed,omitempty"`
	More     []*Managed `json:"more_masters,omitempty"`
	Panel    *Panel     `json:"panel,omitempty"`
	Defaults *Defaults  `json:"defaults,omitempty"`
	// Fetch sends this server's own downloads from listed sites through a
	// tunnel (nil = off).
	Fetch *Fetch `json:"fetch,omitempty"`
}

// Defaults pre-fill the advanced options of new tunnels and forwards.
type Defaults struct {
	Encrypt     string `json:"encrypt,omitempty"`      // IPsec cipher for new tunnels to NeoTunnel servers
	MTUProbe    bool   `json:"mtu_probe,omitempty"`    // measure the path MTU of new tunnels
	Obfs        bool   `json:"obfs,omitempty"`         // run new tunnels over a NeoObfs layer
	Padding     int    `json:"padding,omitempty"`      // NeoObfs random padding
	ForwardMode string `json:"forward_mode,omitempty"` // nat, transparent, proxy, relay
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
	// Domain is the name the panel is opened at, and Cert where its
	// certificate comes from: "acme" (Let's Encrypt, renewed by the daemon),
	// "custom" (pasted in the panel) or "" (self-signed, opened by IP).
	Domain string `json:"domain,omitempty"`
	Cert   string `json:"cert,omitempty"`
}

// Panel certificate sources.
const (
	CertACME   = "acme"
	CertCustom = "custom"
)

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
	Address6 string `json:"address6,omitempty"`
	// AESNI is whether the peer's CPU has AES instructions, as it reports.
	AESNI           *bool     `json:"aes_ni,omitempty"`
	AppliedRevision uint64    `json:"applied_revision,omitempty"`
	LastSeen        time.Time `json:"last_seen,omitempty"`
	// Busy and BusyPorts are what the peer reports taken there by tunnels
	// of its other main servers (inner subnets; "udp/51820" ports and
	// VXLAN/GENEVE ids): new tunnels to it get others.
	Busy      []string `json:"busy_nets,omitempty"`
	BusyPorts []string `json:"busy_ports,omitempty"`
}

func (p Peer) Paired() bool { return p.PublicKey != "" }

type Tunnel struct {
	Name string `json:"name"`
	// Origin, MasterName and MasterKey are set on the copies of a master's
	// tunnels this node runs (see Masters): the name both ends know when
	// Name is a local one, and whose tunnel it is.
	Origin     string `json:"-"`
	MasterName string `json:"-"`
	MasterKey  string `json:"-"`
	Kind       Kind   `json:"kind"`
	// Over names another tunnel whose inner addresses serve as this tunnel's
	// underlay (e.g. ipip6 over sit for the classic "6to4" stack).
	Over  string  `json:"over,omitempty"`
	Sides [2]Side `json:"sides"`

	Net4 string `json:"net4,omitempty"` // inner IPv4 subnet, side 0 = .1, side 1 = .2
	Net6 string `json:"net6,omitempty"` // inner IPv6 subnet, side 0 = ::1, side 1 = ::2

	Key uint32 `json:"key,omitempty"` // GRE key
	VNI uint32 `json:"vni,omitempty"` // VXLAN / GENEVE id
	// TunnelID is EoIP's tunnel id, set to match a MikroTik far end's
	// tunnel-id; nil derives it from Key. It must fit 12 bits on raw EoIPv6.
	TunnelID *uint16 `json:"tunnel_id,omitempty"`
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
	// Profile tunes the tunnel for its traffic: ProfileLean, ProfileMax, or
	// "" for balanced. Set on the user-facing tunnel; it applies to every
	// layer of its stack.
	Profile string `json:"profile,omitempty"`
	// Lanes carries the tunnel over this many parallel tunnels between the
	// same two servers (0 or 1 = one), each inner flow pinned to one lane,
	// so the work spreads over CPU cores; see LaneKinds.
	Lanes int `json:"lanes,omitempty"`
}

// MaxLanes is the most lanes a tunnel can have (lane interfaces are named
// nt-<name>.<n>, which must fit 15 characters).
const MaxLanes = 8

// LaneCount is the number of parallel tunnels the tunnel runs on (>= 1).
func (t Tunnel) LaneCount() int {
	if t.Lanes < 1 {
		return 1
	}
	return t.Lanes
}

// LaneKinds reports whether a kind can run on several lanes: the kernel GRE
// family, whose lanes are told apart by their keys. (Keyless SIT and IPIP
// allow one tunnel per address pair; EoIP's user-space carrier is per tunnel.)
func LaneKinds(k Kind) bool {
	switch k {
	case KindGRE, KindGRETAP, KindIP6GRE, KindIP6GRETAP:
		return true
	}
	return false
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
	//   relay          TCP only, without the tunnel's packets: each user
	//                  connection is carried to the far node as its own TCP
	//                  connection (port 7712, keyed by the tunnel's secret,
	//                  encrypted when the tunnel is), and the far node
	//                  connects to the port on its own loopback. A PROXY
	//                  header is sent when ProxyVersion is 1 or 2.
	//   socks          a SOCKS5 and HTTP proxy on this node (one port, both
	//                  protocols) for programs here: each connection goes
	//                  through the tunnel to the far node like a relay one,
	//                  and leaves to the internet from there.
	Mode string `json:"mode,omitempty"`
	// ProxyVersion is the PROXY protocol version: 2 (default, TCP and UDP)
	// or 1 (text, TCP only). In relay mode 0 sends no header.
	ProxyVersion int `json:"proxy_version,omitempty"`
	// Backup names a second tunnel to the same far node: when Tunnel stops
	// passing traffic the daemon moves the forward there, and back once
	// Tunnel is healthy again (see package failover).
	Backup string `json:"backup,omitempty"`
	// Listen is the address a socks forward listens on: "" = 127.0.0.1,
	// only programs on this server. Any other needs Auth.
	Listen string `json:"listen,omitempty"`
	// Auth is "user:password" for a socks forward ("" = none).
	Auth string `json:"auth,omitempty"`
	// Fetch marks the socks forward a main server sends its peer for its
	// downloads through the tunnel (see Fetch); it has no listener there.
	Fetch bool `json:"fetch,omitempty"`
}

// Uses reports whether the forward can run on the named tunnel.
func (f Forward) Uses(tunnel string) bool {
	return f.Tunnel == tunnel || (f.Backup != "" && f.Backup == tunnel)
}

// Forward modes.
const (
	ForwardNAT         = "nat"
	ForwardTransparent = "transparent"
	ForwardProxy       = "proxy"
	ForwardRelay       = "relay"
	ForwardSocks       = "socks"
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
	case ForwardRelay:
		if version != 0 && version != 1 && version != 2 {
			return fmt.Errorf("PROXY protocol version must be 0 (none), 1 or 2")
		}
		if proto != "tcp" {
			return fmt.Errorf("relay mode carries TCP only; forward UDP ports in another mode")
		}
	case ForwardSocks:
		if proto != "tcp" {
			return fmt.Errorf("a SOCKS/HTTP proxy listens on TCP only")
		}
	default:
		return fmt.Errorf("bad forward mode %q (nat, transparent, proxy, relay, socks)", mode)
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
		// replies back through the tunnel, and of a relay forward to accept
		// its connections (a socks one too).
		if f.Node == peer || ((f.ModeOf() == ForwardTransparent || f.ModeOf() == ForwardRelay || f.ModeOf() == ForwardSocks) && (touches[f.Tunnel] || touches[f.Backup])) {
			fs = append(fs, f)
		}
	}
	if f, ok := s.fetchForward(peer); ok {
		fs = append(fs, f)
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
	ts, fs, _ = s.effective()
	return ts, fs
}

// ReturnPaths lists this node's tunnels that carry transparent forwards from
// the other side: replies to those clients must go back through the tunnel.
func (s *State) ReturnPaths() []string {
	fs := s.AllForwards()
	ts, _ := s.Effective()
	seen := map[string]bool{}
	var out []string
	for _, f := range fs {
		if f.ModeOf() != ForwardTransparent || s.IsSelf(f.Node) {
			continue
		}
		// The far side may move the forward to its backup at any time, so
		// both tunnels need a return path.
		for _, name := range []string{f.Tunnel, f.Backup} {
			if name == "" || seen[name] {
				continue
			}
			for _, t := range ts {
				if t.Name == name {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// IsSelf reports whether a node name in a definition refers to this node,
// either by our own name or by a name one of our masters gave us.
func (s *State) IsSelf(node string) bool {
	if node == s.Self {
		return true
	}
	for _, m := range s.Masters() {
		if node == m.Self {
			return true
		}
	}
	return false
}

// Speed profiles.
const (
	// ProfileLean keeps queues short and uses few connections: lowest
	// latency and the least CPU, for light or interactive traffic.
	ProfileLean = "lean"
	// ProfileMax spreads the tunnel's work over every CPU core and uses the
	// most connections: the highest throughput for heavy traffic.
	ProfileMax = "max"
)

// ValidProfile reports whether p names a profile ("" is balanced).
func ValidProfile(p string) bool {
	return p == "" || p == "balanced" || p == ProfileLean || p == ProfileMax
}
