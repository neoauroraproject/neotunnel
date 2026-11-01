package config

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"unicode/utf8"
)

// AddressPool is a named range new tunnels take their inner addresses from:
// a /30 per tunnel out of Net4 and a /64 out of Net6 (a /126 when Net6 is
// narrower than a /64). A family left empty uses the default for it.
type AddressPool struct {
	Name string `json:"name"`
	Net4 string `json:"net4,omitempty"`
	Net6 string `json:"net6,omitempty"`
}

// DefaultPool is the name of the pool tunnels use unless told otherwise:
// Pool4, and this node's ULA for IPv6.
const DefaultPool = "default"

var builtinPools = []AddressPool{
	{Name: DefaultPool, Net4: Pool4.String()},
	{Name: "172", Net4: "172.27.0.0/16"},
	{Name: "192", Net4: "192.168.77.0/24"},
	{Name: "100", Net4: "100.77.0.0/16"},
}

// BuiltinPool reports whether name is a ready-made pool, which can't be
// changed or removed.
func BuiltinPool(name string) bool {
	return slices.ContainsFunc(builtinPools, func(p AddressPool) bool { return p.Name == name })
}

// AllPools lists the ready-made pools, then this node's own.
func (s *State) AllPools() []AddressPool {
	return append(slices.Clone(builtinPools), s.Pools...)
}

// LookupPool finds a pool by name.
func (s *State) LookupPool(name string) (AddressPool, bool) {
	for _, p := range s.AllPools() {
		if p.Name == name {
			return p, true
		}
	}
	return AddressPool{}, false
}

// ValidPoolName allows any language, within reason: the name only shows in
// menus and is never part of an interface.
func ValidPoolName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || utf8.RuneCountInString(name) > 32 || !utf8.ValidString(name) {
		return fmt.Errorf("a pool's name must be 1-32 characters with no spaces around it")
	}
	return nil
}

// ValidPoolRange checks one range of a pool, of family 4 or 6, and returns
// it in canonical form ("" stays "").
func ValidPoolRange(s string, family int) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a range like 10.20.0.0/16", s)
	}
	p = p.Masked()
	a := p.Addr()
	if (family == 4) != a.Is4() {
		return "", fmt.Errorf("%s is not an IPv%d range", p, family)
	}
	longest := 30
	if family == 6 {
		longest = 126
	}
	if p.Bits() < 8 || p.Bits() > longest {
		return "", fmt.Errorf("%s: an IPv%d range must be between /8 and /%d", p, family, longest)
	}
	if a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return "", fmt.Errorf("%s can't carry a tunnel's addresses", p)
	}
	return p.String(), nil
}

// Check validates a pool's ranges and puts them in canonical form.
func (p *AddressPool) Check() error {
	var err error
	if p.Net4, err = ValidPoolRange(p.Net4, 4); err != nil {
		return err
	}
	if p.Net6, err = ValidPoolRange(p.Net6, 6); err != nil {
		return err
	}
	if p.Net4 == "" && p.Net6 == "" {
		return fmt.Errorf("give an IPv4 or IPv6 range")
	}
	return nil
}

// block4 is the i-th /30 of a range (ok false past its end).
func block4(pool netip.Prefix, i uint32) (netip.Prefix, bool) {
	if uint64(i) >= 1<<(30-pool.Bits()) {
		return netip.Prefix{}, false
	}
	base := pool.Masked().Addr().As4()
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], binary.BigEndian.Uint32(base[:])+i*4)
	return netip.PrefixFrom(netip.AddrFrom4(a), 30), true
}

// block6 is the i-th block of a range: /64s, or /126s when it is narrower
// than a /64 (ok false past its end).
func block6(pool netip.Prefix, i uint64) (netip.Prefix, bool) {
	base := pool.Masked().Addr().As16()
	hi, lo := binary.BigEndian.Uint64(base[:8]), binary.BigEndian.Uint64(base[8:])
	bits, size := pool.Bits(), 64
	if bits <= 64 {
		if 64-bits < 63 && i >= 1<<(64-bits) {
			return netip.Prefix{}, false
		}
		hi += i
	} else {
		if i >= 1<<(126-bits) {
			return netip.Prefix{}, false
		}
		size = 126
		lo += i * 4
	}
	var a [16]byte
	binary.BigEndian.PutUint64(a[:8], hi)
	binary.BigEndian.PutUint64(a[8:], lo)
	return netip.PrefixFrom(netip.AddrFrom16(a), size), true
}

// HostNets lists the networks on this server's interfaces, which a tunnel
// from a chosen pool must stay off. Tests replace it.
var HostNets = func() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			ones, _ := n.Mask.Size()
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				out = append(out, netip.PrefixFrom(ip.Unmap(), ones).Masked())
			}
		}
	}
	return out
}
