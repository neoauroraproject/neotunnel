// Package manage holds every change a user can make — peers, tunnels,
// forwards — shared by the CLI and the web panel. Each change is saved,
// applied on this node and pushed to the peers it touches; peers that can't
// be reached get a setup command instead.
package manage

import (
	"context"
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
		if err := fn(s); err != nil {
			return err
		}
		s.Revision++
		return nil
	})
	if err != nil {
		return nil, Change{}, err
	}
	var ch Change
	for _, e := range engine.Apply(st).Errors {
		ch.Errors = append(ch.Errors, e.Error())
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
	if st.Self != "" && st.Address != "" {
		return st, nil
	}
	return config.Update(func(s *config.State) error {
		if s.Self == "" {
			s.Self = DefaultNodeName()
		}
		if s.Address == "" {
			s.Address = fleet.DetectAddress("")
		}
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

// AddForward forwards ports on this node through a tunnel.
func AddForward(tunnel, ports, proto string, to uint16) (Change, error) {
	if _, err := forward.ParsePorts(ports); err != nil {
		return Change{}, err
	}
	if proto == "" {
		proto = "both"
	}
	_, ch, err := Commit(nil, func(s *config.State) error {
		if _, ok := s.Tunnel(tunnel); !ok {
			return fmt.Errorf("unknown tunnel %q", tunnel)
		}
		if err := portsFree(s, ports, proto, ""); err != nil {
			return err
		}
		s.Forwards = append(s.Forwards, config.Forward{Node: s.Self, Tunnel: tunnel, Proto: proto, Ports: ports, To: to})
		return nil
	})
	return ch, err
}

// RemoveForward deletes the forward of ports on tunnel.
func RemoveForward(tunnel, ports string) (Change, error) {
	_, ch, err := Commit(nil, func(s *config.State) error {
		var fs []config.Forward
		found := false
		for _, f := range s.Forwards {
			if f.Tunnel == tunnel && f.Ports == ports {
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
