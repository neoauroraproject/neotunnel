// Package manage holds every change a user can make — peers, tunnels,
// forwards — shared by the CLI and the web panel. Each change is saved,
// applied on this node and pushed to the peers it touches; peers that can't
// be reached get a setup command instead.
package manage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/fleet"
	"neotunnel/internal/forward"
	"neotunnel/internal/identity"
	"neotunnel/internal/service"
)

// PeerSync is the outcome of pushing a change to one peer.
type PeerSync struct {
	Peer     string       `json:"peer"`
	OK       bool         `json:"ok"`
	Revision uint64       `json:"revision,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Error    string       `json:"error,omitempty"`
	Setup    *fleet.Setup `json:"setup,omitempty"` // what to run on the peer when unreachable
}

// Change reports how a change landed on this node and its peers.
type Change struct {
	Errors []string   `json:"errors,omitempty"` // problems applying on this node
	Peers  []PeerSync `json:"peers,omitempty"`
}

// Commit saves fn's change (bumping the revision), applies it here and pushes
// it to peers.
func Commit(peers []string, fn func(*config.State) error) (*config.State, Change, error) {
	st, err := config.Update(func(s *config.State) error {
		// Peers whose own configuration this change leaves alone stay in
		// sync: the revision is global, but they have nothing new to apply.
		before := map[string]string{}
		for _, p := range s.Peers {
			if p.AppliedRevision == s.Revision {
				before[p.Name] = peerConfig(s, p.Name)
			}
		}
		if err := fn(s); err != nil {
			return err
		}
		s.Revision++
		for i := range s.Peers {
			p := &s.Peers[i]
			if was, ok := before[p.Name]; ok && was == peerConfig(s, p.Name) {
				p.AppliedRevision = s.Revision
			}
		}
		return nil
	})
	if err != nil {
		return nil, Change{}, err
	}
	var ch Change
	for _, e := range engine.Apply(st).Errors {
		ch.Errors = append(ch.Errors, e.Error())
	}
	if !engine.InDaemon() {
		// NeoObfs and EoIP tunnels and PROXY protocol relays live in the daemon.
		service.Reload()
	}
	ch.Peers = Sync(st, peers)
	return st, ch, nil
}

// Sync pushes the current configuration to peers.
func Sync(st *config.State, peers []string) []PeerSync {
	if len(peers) == 0 {
		return nil
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return []PeerSync{{Error: err.Error()}}
	}
	var out []PeerSync
	seen := map[string]bool{}
	for _, p := range peers {
		if seen[p] {
			continue
		}
		seen[p] = true
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := fleet.Push(ctx, id, st, p)
		cancel()
		r := PeerSync{Peer: p, OK: err == nil, Revision: resp.Revision, Warnings: resp.Errors}
		if err != nil {
			r.Error = err.Error()
			if s, serr := fleet.MakeSetup(id, p); serr == nil {
				r.Setup = &s
			}
		}
		out = append(out, r)
	}
	return out
}

// Node initializes this node's name and public address on first use.
func Node() (*config.State, error) {
	st, err := config.Load()
	if err != nil {
		return nil, err
	}
	if st.Self != "" && st.Address != "" && st.AgentPort != 0 {
		return st, nil
	}
	return config.Update(func(s *config.State) error {
		if s.Self == "" {
			s.Self = DefaultNodeName()
		}
		if s.Address == "" {
			s.Address = fleet.DetectAddress("")
		}
		// Setup links name this port, so it is picked before the first one
		// (the service listens on whatever is saved here).
		config.EnsureAgentPort(s, config.TCPPortFree)
		return nil
	})
}

var (
	nodeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	cleanRe    = regexp.MustCompile(`[^a-z0-9-]+`)
)

func ValidNodeName(n string) error {
	if !nodeNameRe.MatchString(n) {
		return fmt.Errorf("invalid name %q: use a-z, 0-9 and '-' (max 32)", n)
	}
	return nil
}

func DefaultNodeName() string {
	h, _ := os.Hostname()
	n := strings.Trim(cleanRe.ReplaceAllString(strings.ToLower(h), "-"), "-")
	if len(n) > 32 {
		n = n[:32]
	}
	if n == "" {
		n = "node"
	}
	return n
}

// SetNode changes this node's name and/or public address.
func SetNode(name, address string) error {
	if name != "" {
		if err := ValidNodeName(name); err != nil {
			return err
		}
	}
	if address != "" {
		if _, err := netip.ParseAddr(address); err != nil {
			return fmt.Errorf("address must be an IP: %w", err)
		}
	}
	_, err := config.Update(func(s *config.State) error {
		if name != "" && name != s.Self {
			if len(s.Tunnels) > 0 {
				return fmt.Errorf("remove tunnels before renaming this node")
			}
			s.Self = name
		}
		if address != "" {
			s.Address = address
		}
		return nil
	})
	return err
}

// AddPeer registers a server this node will manage.
func AddPeer(name, address string) error {
	if err := ValidNodeName(name); err != nil {
		return err
	}
	if _, err := netip.ParseAddr(address); err != nil {
		return fmt.Errorf("address must be an IP: %w", err)
	}
	_, err := config.Update(func(s *config.State) error {
		if name == s.Self {
			return fmt.Errorf("%q is this server's own name", name)
		}
		if _, ok := s.Peer(name); ok {
			return fmt.Errorf("server %q already exists", name)
		}
		for _, p := range s.Peers {
			if p.Address == address {
				return fmt.Errorf("server %s already uses %s", p.Name, address)
			}
		}
		s.Peers = append(s.Peers, config.Peer{Name: name, Address: address, AgentPort: agent.DefaultPort})
		return nil
	})
	return err
}

// RemovePeer forgets a peer and removes this node's tunnels to it.
func RemovePeer(name string) (Change, error) {
	_, ch, err := Commit(nil, func(s *config.State) error {
		if _, ok := s.Peer(name); !ok {
			return fmt.Errorf("unknown server %q", name)
		}
		var ps []config.Peer
		for _, p := range s.Peers {
			if p.Name != name {
				ps = append(ps, p)
			}
		}
		s.Peers = ps
		var drop []string
		for _, t := range s.Tunnels {
			if _, ok := t.SideOf(name); ok {
				drop = append(drop, t.Name)
			}
		}
		removeTunnels(s, drop)
		return nil
	})
	return ch, err
}

// Setup returns the commands that bring a peer to this node's configuration.
func Setup(peer string) (fleet.Setup, error) {
	if _, err := Node(); err != nil {
		return fleet.Setup{}, err
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return fleet.Setup{}, err
	}
	return fleet.MakeSetup(id, peer)
}

// ForwardRequest forwards ports on this node through a tunnel.
type ForwardRequest struct {
	Tunnel       string `json:"tunnel"`
	Ports        string `json:"ports"`
	Proto        string `json:"proto,omitempty"` // tcp, udp, both (default)
	To           uint16 `json:"to,omitempty"`
	Mode         string `json:"mode,omitempty"` // nat (default), transparent, proxy, relay
	ProxyVersion int    `json:"proxy_version,omitempty"`
	// Backup is a second tunnel to the same far server that the forward
	// moves to while Tunnel doesn't pass traffic ("" = none).
	Backup string `json:"backup,omitempty"`
	// Listen and Auth are a socks forward's address ("" = 127.0.0.1) and
	// "user:password" ("" = none; required off loopback).
	Listen string `json:"listen,omitempty"`
	Auth   string `json:"auth,omitempty"`
}

func (r *ForwardRequest) check(st *config.State) error {
	if _, err := forward.ParsePorts(r.Ports); err != nil {
		return err
	}
	for _, port := range forward.Expand(r.Ports) {
		for _, p := range st.AgentPorts() {
			if port == p {
				return fmt.Errorf("port %d is reserved for neotunnel agent", p)
			}
		}
	}
	if r.Mode == "" {
		r.Mode = st.D().ForwardMode
	}
	if r.Proto == "" {
		r.Proto = "both"
		if r.Mode == config.ForwardRelay || r.Mode == config.ForwardSocks {
			r.Proto = "tcp"
		}
	}
	if err := config.ValidForwardMode(r.Mode, r.ProxyVersion, r.Proto); err != nil {
		return err
	}
	t, ok := st.Tunnel(r.Tunnel)
	if !ok {
		return fmt.Errorf("unknown tunnel %q", r.Tunnel)
	}
	if r.Mode == config.ForwardTransparent && t.Sides[1].Node == "" {
		return fmt.Errorf("transparent forwarding needs NeoTunnel on the far end to route replies back")
	}
	if r.Mode == config.ForwardRelay && t.Sides[1].Node == "" {
		return fmt.Errorf("relay mode needs NeoTunnel on the far end to accept the connections")
	}
	if r.Mode == config.ForwardSocks {
		if err := checkSocks(r, *t); err != nil {
			return err
		}
	} else {
		r.Listen, r.Auth = "", ""
	}
	if (r.Mode == config.ForwardProxy || r.Mode == config.ForwardRelay) && len(forward.Expand(r.Ports)) > forward.MaxRelayPorts {
		return fmt.Errorf("proxy mode supports up to %d ports per forward", forward.MaxRelayPorts)
	}
	if r.Backup != "" {
		if err := checkBackup(st, *t, r.Backup); err != nil {
			return err
		}
	}
	return nil
}

// checkSocks checks a SOCKS/HTTP proxy: one port, NeoTunnel on the far end
// (its connections leave from there), and a password unless only programs
// on this server can reach it.
func checkSocks(r *ForwardRequest, t config.Tunnel) error {
	if len(forward.Expand(r.Ports)) != 1 {
		return fmt.Errorf("a proxy listens on one port")
	}
	if t.Sides[1].Node == "" {
		return fmt.Errorf("a proxy needs NeoTunnel on the far end: its connections leave from there")
	}
	if r.Listen != "" {
		a, err := netip.ParseAddr(r.Listen)
		if err != nil {
			return fmt.Errorf("listen address %q is not an IP address", r.Listen)
		}
		if a == netip.AddrFrom4([4]byte{127, 0, 0, 1}) {
			r.Listen = ""
		} else if r.Auth == "" && !a.IsLoopback() {
			return fmt.Errorf("a proxy others can reach needs a user and password (user:password)")
		}
	}
	if r.Auth != "" {
		if u, p, ok := strings.Cut(r.Auth, ":"); !ok || u == "" || p == "" || len(u) > 255 || len(p) > 255 {
			return fmt.Errorf("auth must be user:password")
		}
	}
	return nil
}

// checkBackup checks that backup can stand in for tunnel t: another tunnel
// between this server and the same far server.
func checkBackup(st *config.State, t config.Tunnel, backup string) error {
	b, ok := st.Tunnel(backup)
	if !ok {
		return fmt.Errorf("unknown backup tunnel %q", backup)
	}
	if b.Name == t.Name {
		return fmt.Errorf("the backup must be another tunnel than %s", t.Name)
	}
	if _, ok := b.SideOf(st.Self); !ok {
		return fmt.Errorf("backup tunnel %s does not start on this server", backup)
	}
	if farOf(st, *b) != farOf(st, t) {
		return fmt.Errorf("backup tunnel %s goes to %s, not to %s like %s", backup, farOf(st, *b), farOf(st, t), t.Name)
	}
	return nil
}

// farOf names the tunnel's far server: its node, or its address when it
// doesn't run NeoTunnel.
func farOf(st *config.State, t config.Tunnel) string {
	me, _ := t.SideOf(st.Self)
	if far := t.Sides[1-me]; far.Node != "" {
		return far.Node
	}
	return t.Sides[1-me].Endpoint
}

// AddForward forwards ports on this node through a tunnel. The tunnel's far
// end is updated too: a transparent forward needs its return path there.
func AddForward(r ForwardRequest) (Change, error) {
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	if err := r.check(st); err != nil {
		return Change{}, err
	}
	t, _ := st.Tunnel(r.Tunnel)
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error {
		t, ok := s.Tunnel(r.Tunnel)
		if !ok {
			return fmt.Errorf("unknown tunnel %q", r.Tunnel)
		}
		relaySecret(t, r.Mode)
		if b, ok := s.Tunnel(r.Backup); ok {
			relaySecret(b, r.Mode)
		}
		if err := portsFree(s, r.Ports, r.Proto, ""); err != nil {
			return err
		}
		s.Forwards = append(s.Forwards, config.Forward{Node: s.Self, Tunnel: r.Tunnel, Proto: r.Proto, Ports: r.Ports,
			To: r.To, Mode: r.Mode, ProxyVersion: r.ProxyVersion, Backup: r.Backup, Listen: r.Listen, Auth: r.Auth})
		return nil
	})
	return ch, err
}

// UpdateForward changes how the forward of ports on tunnel reaches the far
// end (mode, PROXY protocol version, target port, protocol).
func UpdateForward(r ForwardRequest) (Change, error) {
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	if err := r.check(st); err != nil {
		return Change{}, err
	}
	t, _ := st.Tunnel(r.Tunnel)
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error {
		for i, f := range s.Forwards {
			if f.Node == s.Self && f.Tunnel == r.Tunnel && f.Ports == r.Ports {
				if t, ok := s.Tunnel(r.Tunnel); ok {
					relaySecret(t, r.Mode)
				}
				if b, ok := s.Tunnel(r.Backup); ok {
					relaySecret(b, r.Mode)
				}
				f.Proto, f.To, f.Mode, f.ProxyVersion, f.Backup = r.Proto, r.To, r.Mode, r.ProxyVersion, r.Backup
				f.Listen, f.Auth = r.Listen, r.Auth
				s.Forwards[i] = f
				return portsFreeExcept(s, i)
			}
		}
		return fmt.Errorf("no forward of %s on %s", r.Ports, r.Tunnel)
	})
	return ch, err
}

// relaySecret gives a tunnel the secret its relay forwards' keys come from
// (tunnels that encrypt nothing have none).
func relaySecret(t *config.Tunnel, mode string) {
	if (mode == config.ForwardRelay || mode == config.ForwardSocks) && t.Secret == "" {
		t.Secret = config.NewSecret()
	}
}

// portsFreeExcept checks forward i against the others on this node.
func portsFreeExcept(s *config.State, i int) error {
	f := s.Forwards[i]
	others := append(append([]config.Forward(nil), s.Forwards[:i]...), s.Forwards[i+1:]...)
	saved := s.Forwards
	s.Forwards = others
	err := portsFree(s, f.Ports, f.Proto, "")
	s.Forwards = saved
	return err
}

// RemoveForward deletes the forward of ports on tunnel.
func RemoveForward(tunnel, ports string) (Change, error) {
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	var peers []string
	if t, ok := st.Tunnel(tunnel); ok {
		peers = st.PeersOf(*t)
	}
	_, ch, err := Commit(peers, func(s *config.State) error {
		var fs []config.Forward
		found := false
		for _, f := range s.Forwards {
			if f.Node == s.Self && f.Tunnel == tunnel && f.Ports == ports {
				found = true
				continue
			}
			fs = append(fs, f)
		}
		if !found {
			return fmt.Errorf("no forward of %s on %s", ports, tunnel)
		}
		s.Forwards = fs
		return nil
	})
	return ch, err
}

// SetDefaults saves the options new tunnels and forwards start with.
func SetDefaults(d config.Defaults) error {
	if d.Encrypt != "" {
		if _, err := config.LookupCipher(d.Encrypt); err != nil {
			return err
		}
	}
	if d.Padding < 0 || d.Padding > 255 {
		return fmt.Errorf("padding must be 0-255 bytes")
	}
	if err := config.ValidForwardMode(d.ForwardMode, 0, "both"); err != nil {
		return err
	}
	_, err := config.Update(func(s *config.State) error {
		s.Defaults = &d
		return nil
	})
	return err
}

// portsFree rejects forwarding a port twice on this node.
func portsFree(s *config.State, ports, proto, skipTunnel string) error {
	want := expand(ports)
	for _, f := range s.Forwards {
		if f.Node != s.Self || f.Tunnel == skipTunnel || !protoOverlap(f.Proto, proto) {
			continue
		}
		for p := range expand(f.Ports) {
			if want[p] {
				return fmt.Errorf("port %d is already forwarded to %s", p, f.Tunnel)
			}
		}
	}
	return nil
}

func protoOverlap(a, b string) bool {
	return a == b || a == "both" || b == "both" || a == "" || b == ""
}

func expand(ports string) map[int]bool {
	out := map[int]bool{}
	for _, f := range strings.Split(ports, ",") {
		var lo, hi int
		if n, _ := fmt.Sscanf(strings.TrimSpace(f), "%d-%d", &lo, &hi); n == 2 {
			for p := lo; p <= hi && p-lo < 65536; p++ {
				out[p] = true
			}
		} else if n == 1 {
			out[lo] = true
		}
	}
	return out
}

// peerConfig is what a peer receives, for telling whether a change concerns it.
func peerConfig(s *config.State, peer string) string {
	ts, fs := s.ForPeer(peer)
	b, _ := json.Marshal(struct {
		T []config.Tunnel
		F []config.Forward
	}{ts, fs})
	return string(b)
}
