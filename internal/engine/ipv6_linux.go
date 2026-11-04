package engine

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

// Many servers run with IPv6 off (net.ipv6.conf.all/default.disable_ipv6
// = 1). Interfaces made there start with IPv6 off too, and the kernel then
// refuses a tunnel's inner IPv6 addresses with "permission denied", which
// also left the tunnel down, and a SIT layer (6to4) can't carry anything.
// So IPv6 is turned on for NeoTunnel's own interfaces only; the server's
// other interfaces stay as they are.

// errNoIPv6: the kernel itself has no IPv6 (booted with ipv6.disable=1).
var errNoIPv6 = errors.New("this server's kernel runs without IPv6 (booted with ipv6.disable=1)")

// ipv6Conf is where the kernel keeps an interface's IPv6 settings (tests
// replace it).
var ipv6Conf = "/proc/sys/net/ipv6/conf"

// enableIPv6 turns IPv6 on for the interface name when it is off.
func enableIPv6(name string) error {
	if _, err := os.Stat(ipv6Conf); err != nil {
		return errNoIPv6
	}
	p := ipv6Conf + "/" + name + "/disable_ipv6"
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("IPv6 on %s: %w", name, err)
	}
	if strings.TrimSpace(string(b)) == "0" {
		return nil
	}
	if err := os.WriteFile(p, []byte("0"), 0o644); err != nil {
		return fmt.Errorf("turn IPv6 on for %s: %w", name, err)
	}
	return nil
}

// usableAddrs are the addresses of want the interface name can take: all
// of them once IPv6 is on there, the IPv4 ones alone where the kernel has
// no IPv6 (err then says why the others were left out).
func usableAddrs(name string, want []netip.Prefix) ([]netip.Prefix, error) {
	if !slicesHas6(want) {
		return want, nil
	}
	err := enableIPv6(name)
	if err == nil {
		return want, nil
	}
	var v4 []netip.Prefix
	for _, p := range want {
		if p.Addr().Is4() {
			v4 = append(v4, p)
		}
	}
	return v4, fmt.Errorf("%s: no inner IPv6 address: %w", name, err)
}

func slicesHas6(ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Addr().Is6() && !p.Addr().Is4In6() {
			return true
		}
	}
	return false
}
