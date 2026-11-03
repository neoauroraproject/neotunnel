package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
)

// Pool4 is where inner IPv4 /30s are carved from.
var Pool4 = netip.MustParsePrefix("10.77.0.0/16")

// ULA returns this node's IPv6 ULA /48 (RFC 4193), creating it on first use.
func (s *State) ULA() netip.Prefix {
	if p, err := netip.ParsePrefix(s.ULA48); err == nil {
		return p
	}
	var b [16]byte
	b[0] = 0xfd
	rand.Read(b[1:6])
	p := netip.PrefixFrom(netip.AddrFrom16(b), 48)
	s.ULA48 = p.String()
	return p
}

func (s *State) usedNets() map[netip.Prefix]bool {
	used := map[netip.Prefix]bool{}
	ts, _ := s.Effective()
	ts = append(ts, s.Tunnels...)
	for _, t := range ts {
		for _, n := range []string{t.Net4, t.Net6} {
			if p, err := netip.ParsePrefix(n); err == nil {
				used[p.Masked()] = true
			}
		}
	}
	return used
}

// Allocate fills in inner subnets for a new tunnel based on what it carries.
func (s *State) Allocate(t *Tunnel) error {
	ki, err := LookupKind(t.Kind)
	if err != nil {
		return err
	}
	used := s.usedNets()
	// Subnets the far server already uses for another main server's tunnels.
	busy, _ := s.peerBusy(*t)
	var host []netip.Prefix
	if t.Pool4 != "" || t.Pool6 != "" {
		host = HostNets() // a chosen range may be one this server already sits on
	}
	taken := func(p netip.Prefix) bool {
		for u := range used {
			if u.Overlaps(p) {
				return true
			}
		}
		for _, b := range busy {
			if b.Overlaps(p) {
				return true
			}
		}
		for _, h := range host {
			if h.Overlaps(p) {
				return true
			}
		}
		return false
	}
	if ki.Stream {
		t.Net4, t.Net6 = "", "" // connection tunnels have no inner addresses
	}
	if ki.Carry4 && t.Net4 == "" {
		pool := Pool4
		if t.Pool4 != "" {
			if pool, err = netip.ParsePrefix(t.Pool4); err != nil {
				return fmt.Errorf("IPv4 pool %q: %w", t.Pool4, err)
			}
		}
		for i := uint32(0); ; i++ {
			p, ok := block4(pool, i)
			if !ok {
				return fmt.Errorf("IPv4 pool %s has no free /30 left", pool)
			}
			if !taken(p) {
				t.Net4 = p.String()
				used[p] = true
				break
			}
		}
	}
	if ki.Carry6 && t.Net6 == "" {
		if t.Pool6 != "" {
			pool, err := netip.ParsePrefix(t.Pool6)
			if err != nil {
				return fmt.Errorf("IPv6 pool %q: %w", t.Pool6, err)
			}
			for i := uint64(0); i < 1<<16; i++ {
				p, ok := block6(pool, i)
				if !ok {
					break
				}
				if !taken(p) {
					t.Net6 = p.String()
					break
				}
			}
			if t.Net6 == "" {
				return fmt.Errorf("IPv6 pool %s has no free block left", pool)
			}
		} else {
			ula := s.ULA().Addr().As16()
			for i := uint16(1); i != 0; i++ {
				a := ula
				binary.BigEndian.PutUint16(a[6:8], i)
				p := netip.PrefixFrom(netip.AddrFrom16(a), 64)
				if !taken(p) {
					t.Net6 = p.String()
					break
				}
			}
		}
	}
	if t.Key == 0 {
		t.Key = keyFor(t.Name)
	}
	if (t.Kind == KindVXLAN || t.Kind == KindGENEVE) && t.VNI == 0 {
		t.VNI = keyFor(t.Name) & 0xffffff
		if _, _, vni := s.PeerClash(*t); vni {
			s.NewVNI(t)
		}
	}
	if t.NeedsSecret() && t.Secret == "" {
		t.Secret = NewSecret()
	}
	if ki.Stream {
		s.EnsureRelayPorts(t)
	}
	return nil
}

// keyFor derives a stable non-zero GRE key / VNI from a tunnel name.
func keyFor(name string) uint32 {
	h := sha256.Sum256([]byte("neotunnel/" + name))
	if k := binary.BigEndian.Uint32(h[:4]); k != 0 {
		return k
	}
	return 1
}

// Inner returns the inner address of a side as a prefix (e.g. 10.77.0.1/30).
func Inner(netStr string, side int) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(netStr)
	if err != nil {
		return netip.Prefix{}, false
	}
	a := p.Masked().Addr()
	for i := 0; i <= side; i++ {
		a = a.Next()
	}
	return netip.PrefixFrom(a, p.Bits()), true
}
