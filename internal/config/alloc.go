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
	if ki.Carry4 && t.Net4 == "" {
		base := Pool4.Addr().As4()
		start := binary.BigEndian.Uint32(base[:])
		size := uint32(1) << (32 - Pool4.Bits())
		for off := uint32(0); off < size; off += 4 {
			var a [4]byte
			binary.BigEndian.PutUint32(a[:], start+off)
			p := netip.PrefixFrom(netip.AddrFrom4(a), 30)
			if !used[p] {
				t.Net4 = p.String()
				used[p] = true
				break
			}
		}
		if t.Net4 == "" {
			return fmt.Errorf("IPv4 pool %s exhausted", Pool4)
		}
	}
	if ki.Carry6 && t.Net6 == "" {
		ula := s.ULA().Addr().As16()
		for i := uint16(1); i != 0; i++ {
			a := ula
			binary.BigEndian.PutUint16(a[6:8], i)
			p := netip.PrefixFrom(netip.AddrFrom16(a), 64)
			if !used[p] {
				t.Net6 = p.String()
				break
			}
		}
	}
	if t.Key == 0 {
		t.Key = keyFor(t.Name)
	}
	if (t.Kind == KindVXLAN || t.Kind == KindGENEVE) && t.VNI == 0 {
		t.VNI = keyFor(t.Name) & 0xffffff
	}
	if t.NeedsSecret() && t.Secret == "" {
		t.Secret = NewSecret()
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
