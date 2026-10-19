package config

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"strings"
)

// A node can be managed by several main servers (one Iran server to many
// abroad ones, and each abroad one to many Iran ones). The first keeps its
// place in State.Managed, where older versions look for it; the others are
// in State.More.
//
// Each master names its tunnels on its own, so two of them may both have a
// "t1". On this node the tunnels of the first master keep their names; those
// of the others get a local name with a tag of their master (LocalName),
// used for interfaces and everything else local. The name both ends agree
// on, which keys and wire protocols use, stays in Origin (KeyName).

// Masters lists the servers that manage this node, the first first.
func (s *State) Masters() []*Managed {
	var out []*Managed
	if s.Managed != nil {
		out = append(out, s.Managed)
	}
	for _, m := range s.More {
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

// MasterByKey finds the master with this public key.
func (s *State) MasterByKey(key string) *Managed {
	for _, m := range s.Masters() {
		if m.Master.PublicKey == key {
			return m
		}
	}
	return nil
}

// AddMaster adds a master: the first one, or one more.
func (s *State) AddMaster(m *Managed) {
	if s.Managed == nil {
		s.Managed = m
		return
	}
	s.More = append(s.More, m)
}

// SelfNames are the names this node goes by: its own and each master's.
func (s *State) SelfNames() map[string]bool {
	out := map[string]bool{}
	if s.Self != "" {
		out[s.Self] = true
	}
	for _, m := range s.Masters() {
		out[m.Self] = true
	}
	return out
}

// KeyName is the tunnel's name as both ends know it: keys are derived from
// it and the wire protocols carry it.
func (t Tunnel) KeyName() string {
	if t.Origin != "" {
		return t.Origin
	}
	return t.Name
}

// masterTag is a short, stable tag of a master's key.
func masterTag(key string) string { return shortHash(key, 3) }

func shortHash(s string, n int) string {
	h := sha256.Sum256([]byte(s))
	const digits = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[int(h[i])%len(digits)]
	}
	return string(b)
}

// LocalName is the name on this node of tunnel name from the master with
// key (not the first master): the name and the master's tag when they fit
// in a tunnel name, else a shortened name and a hash of both.
func LocalName(name, key string) string {
	if name == "" {
		return ""
	}
	if tag := masterTag(key); len(name)+1+len(tag) <= 10 {
		return name + "-" + tag
	}
	return strings.TrimRight(name[:4], "-") + "-" + shortHash(key+"/"+name, 5)
}

// view returns a master's tunnels and forwards as this node runs them: with
// local names when rename is set, and their master recorded.
func (m *Managed) view(rename bool) ([]Tunnel, []Forward) {
	local := func(n string) string {
		if !rename || n == "" {
			return n
		}
		return LocalName(n, m.Master.PublicKey)
	}
	ts := make([]Tunnel, 0, len(m.Tunnels))
	for _, t := range m.Tunnels {
		t.MasterName, t.MasterKey = m.Master.Name, m.Master.PublicKey
		if rename {
			t.Origin, t.Name, t.Over = t.Name, local(t.Name), local(t.Over)
		}
		ts = append(ts, t)
	}
	fs := make([]Forward, 0, len(m.Forwards))
	for _, f := range m.Forwards {
		f.Tunnel, f.Backup = local(f.Tunnel), local(f.Backup)
		fs = append(fs, f)
	}
	return ts, fs
}

// managed returns every master's tunnels and forwards with local names, in
// master order, each with the master's name for this node.
func (s *State) managed() (ts []Tunnel, fs []Forward, selfOf []string) {
	for i, m := range s.Masters() {
		mts, mfs := m.view(i > 0)
		for range mts {
			selfOf = append(selfOf, m.Self)
		}
		ts = append(ts, mts...)
		fs = append(fs, mfs...)
	}
	return ts, fs, selfOf
}

// ManagedFor returns the master's tunnel named name (its own name for it)
// as this node runs it.
func (s *State) ManagedFor(key, name string) (Tunnel, bool) {
	for i, m := range s.Masters() {
		if m.Master.PublicKey != key {
			continue
		}
		ts, _ := m.view(i > 0)
		for _, t := range ts {
			if t.KeyName() == name {
				return t, true
			}
		}
	}
	return Tunnel{}, false
}

// AllForwards lists every forward this node knows: its own and its
// masters' (with local tunnel names), on whichever node they run.
func (s *State) AllForwards() []Forward {
	fs := append([]Forward(nil), s.Forwards...)
	_, mfs, _ := s.managed()
	return append(fs, mfs...)
}

// claim is what a tunnel takes on this node that no other may: its inner
// subnets and its port.
type claim struct {
	nets []netip.Prefix
	port string // "udp/51820", "" = none
	vni  string // "vni/vxlan/4789/123" for VXLAN and GENEVE, which share a port
}

func claimOf(t Tunnel) claim {
	var c claim
	for _, n := range []string{t.Net4, t.Net6} {
		if p, err := netip.ParsePrefix(n); err == nil {
			c.nets = append(c.nets, p.Masked())
		}
	}
	if t.Port != 0 {
		proto := "udp"
		if ki, err := LookupKind(t.Kind); err == nil && ki.TCP {
			proto = "tcp"
		}
		c.port = fmt.Sprintf("%s/%d", proto, t.Port)
	}
	if t.Kind == KindVXLAN || t.Kind == KindGENEVE {
		port := t.Port
		if port == 0 {
			port = map[Kind]uint16{KindVXLAN: 4789, KindGENEVE: 6081}[t.Kind]
		}
		c.vni = fmt.Sprintf("vni/%s/%d/%d", t.Kind, port, t.VNI)
	}
	return c
}

func (c claim) clashes(o claim) bool {
	if (c.port != "" && c.port == o.port) || (c.vni != "" && c.vni == o.vni) {
		return true
	}
	for _, a := range c.nets {
		for _, b := range o.nets {
			if a.Overlaps(b) {
				return true
			}
		}
	}
	return false
}

// effective is Effective with the managed tunnels left out because they
// clash with one before them (this node's own first, then each master's in
// order) and why.
func (s *State) effective() (ts []Tunnel, fs []Forward, conflicts []error) {
	type owned struct {
		name, master, key string
		c                 claim
	}
	var taken []owned
	for _, t := range s.Tunnels {
		if _, ok := t.SideOf(s.Self); ok {
			ts = append(ts, t)
			taken = append(taken, owned{t.Name, "", "", claimOf(t)})
		}
	}
	for _, f := range s.Forwards {
		if f.Node == s.Self {
			fs = append(fs, f)
		}
	}
	mts, mfs, selfOf := s.managed()
	for i, t := range mts {
		if _, ok := t.SideOf(selfOf[i]); !ok {
			continue
		}
		c := claimOf(t)
		clash := ""
		for _, o := range taken {
			if o.key != t.MasterKey && c.clashes(o.c) {
				clash = o.name
				if o.master != "" {
					clash += " (from " + o.master + ")"
				}
				break
			}
		}
		if clash != "" {
			conflicts = append(conflicts, fmt.Errorf("tunnel %s from %s: its addresses or port are already used here by tunnel %s; %s picks new ones on its next sync",
				t.KeyName(), t.MasterName, clash, t.MasterName))
			continue
		}
		ts = append(ts, t)
		taken = append(taken, owned{t.Name, t.MasterName, t.MasterKey, c})
	}
	selfNames := s.SelfNames()
	for _, f := range mfs {
		if selfNames[f.Node] {
			fs = append(fs, f)
		}
	}
	return ts, fs, conflicts
}

// Conflicts explains the managed tunnels this node can't run because their
// addresses or ports are taken by another tunnel here.
func (s *State) Conflicts() []error {
	_, _, c := s.effective()
	return c
}

// Busy lists the inner subnets, ports ("udp/51820") and VXLAN/GENEVE ids this node uses for
// tunnels other than those of the master with key: what that master must
// not give its tunnels to us.
func (s *State) Busy(key string) (nets, ports []string) {
	ts, _, _ := s.effective()
	seenN, seenP := map[string]bool{}, map[string]bool{}
	for _, t := range ts {
		if key != "" && t.MasterKey == key {
			continue
		}
		c := claimOf(t)
		for _, n := range c.nets {
			if !seenN[n.String()] {
				seenN[n.String()] = true
				nets = append(nets, n.String())
			}
		}
		for _, k := range []string{c.port, c.vni} {
			if k != "" && !seenP[k] {
				seenP[k] = true
				ports = append(ports, k)
			}
		}
	}
	return nets, ports
}

// peerBusy is what the peers at either end of t report taken by tunnels
// other than ours (see Busy).
func (s *State) peerBusy(t Tunnel) (nets []netip.Prefix, keys map[string]bool) {
	keys = map[string]bool{}
	for _, sd := range t.Sides {
		p, ok := s.Peer(sd.Node)
		if !ok {
			continue
		}
		for _, n := range p.Busy {
			if pr, err := netip.ParsePrefix(n); err == nil {
				nets = append(nets, pr)
			}
		}
		for _, k := range p.BusyPorts {
			keys[k] = true
		}
	}
	return nets, keys
}

// PeerClash reports what of t its far server says is taken there by
// another main server's tunnels: its inner subnets, its port, its VNI.
func (s *State) PeerClash(t Tunnel) (nets, port, vni bool) {
	busy, keys := s.peerBusy(t)
	c := claimOf(t)
	for _, a := range c.nets {
		for _, b := range busy {
			nets = nets || a.Overlaps(b)
		}
	}
	return nets, c.port != "" && keys[c.port], c.vni != "" && keys[c.vni]
}

// PeerTakes reports whether a peer at either end of t says port (TCP when
// tcp) is taken there.
func (s *State) PeerTakes(t Tunnel, port uint16, tcp bool) bool {
	_, keys := s.peerBusy(t)
	proto := "udp"
	if tcp {
		proto = "tcp"
	}
	return keys[fmt.Sprintf("%s/%d", proto, port)]
}

// NewVNI gives a VXLAN or GENEVE tunnel another id when its far server
// reports the current one taken.
func (s *State) NewVNI(t *Tunnel) {
	_, keys := s.peerBusy(*t)
	for i := 1; i < 1000; i++ {
		t.VNI = keyFor(fmt.Sprintf("%s#%d", t.Name, i)) & 0xffffff
		if c := claimOf(*t); !keys[c.vni] {
			return
		}
	}
}
