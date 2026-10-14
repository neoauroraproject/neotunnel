package engine

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"neotunnel/internal/config"
)

// Interface aliases mark what we own:
//
//	neotunnel:<hash>                 persistent tunnel
//	neotunnel-bench:<expiry>:<hash>  temporary test tunnel, removed after expiry
const (
	aliasPersistent = "neotunnel:"
	aliasBench      = "neotunnel-bench:"
)

// ip6tnl flag: don't insert the 8-byte encapsulation-limit option header.
const ip6tnlIgnEncapLimit = 0x1

func toIP(a netip.Addr) net.IP { return net.IP(a.AsSlice()) }

// tunnelEncapFOU is TUNNEL_ENCAP_FOU from linux/if_tunnel.h.
const tunnelEncapFOU = 1

func buildLink(s LinkSpec) (netlink.Link, error) {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = s.Name
	attrs.MTU = s.MTU
	l, r := toIP(s.Local), toIP(s.Remote)
	// With FOU the packet goes out as UDP to Port; source port 0 = automatic.
	var encap, dport uint16
	if s.FOU {
		encap, dport = tunnelEncapFOU, s.Port
	}
	switch s.Kind {
	case config.KindSIT:
		return &netlink.Sittun{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, PMtuDisc: 1, Proto: unix.IPPROTO_IPV6,
			EncapType: encap, EncapDport: dport}, nil
	case config.KindIPIP:
		return &netlink.Iptun{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, PMtuDisc: 1,
			EncapType: encap, EncapDport: dport}, nil
	case config.KindGRE, config.KindIP6GRE:
		return &netlink.Gretun{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, PMtuDisc: 1, IKey: s.Key, OKey: s.Key,
			EncapType: encap, EncapDport: dport}, nil
	case config.KindGRETAP, config.KindIP6GRETAP:
		return &netlink.Gretap{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, PMtuDisc: 1, IKey: s.Key, OKey: s.Key,
			EncapType: encap, EncapDport: dport}, nil
	case config.KindIPIP6:
		return &netlink.Ip6tnl{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, Proto: unix.IPPROTO_IPIP, Flags: ip6tnlIgnEncapLimit}, nil
	case config.KindIP6IP6:
		return &netlink.Ip6tnl{LinkAttrs: attrs, Local: l, Remote: r, Ttl: s.TTL, Proto: unix.IPPROTO_IPV6, Flags: ip6tnlIgnEncapLimit}, nil
	case config.KindVXLAN:
		return &netlink.Vxlan{LinkAttrs: attrs, VxlanId: int(s.VNI), SrcAddr: l, Group: r, Port: int(s.Port), TTL: int(s.TTL), Learning: true}, nil
	case config.KindGENEVE:
		// geneve has no local-address attribute; the kernel picks the source by route.
		return &netlink.Geneve{LinkAttrs: attrs, ID: s.VNI, Remote: r, Dport: s.Port, Ttl: s.TTL}, nil
	}
	return nil, fmt.Errorf("unsupported kind %s", s.Kind)
}

// ensure makes the kernel interface match s. alias is the full alias to set.
// Returns true if the link was (re)created.
func ensure(s LinkSpec, alias string) (bool, error) {
	cur, err := netlink.LinkByName(s.Name)
	created := false
	if err == nil && cur.Attrs().Alias != alias {
		if err := netlink.LinkDel(cur); err != nil {
			return false, fmt.Errorf("remove stale %s: %w", s.Name, err)
		}
		cur = nil
	} else if err != nil {
		var nf netlink.LinkNotFoundError
		if !errors.As(err, &nf) {
			return false, err
		}
		cur = nil
	}
	if cur == nil {
		l, err := buildLink(s)
		if err != nil {
			return false, err
		}
		if err := netlink.LinkAdd(l); err != nil {
			return false, fmt.Errorf("create %s (%s): %w", s.Name, s.Kind, explain(err))
		}
		if cur, err = netlink.LinkByName(s.Name); err != nil {
			return false, err
		}
		if err := netlink.LinkSetAlias(cur, alias); err != nil {
			netlink.LinkDel(cur)
			return false, err
		}
		created = true
	}
	if cur.Attrs().MTU != s.MTU {
		if err := netlink.LinkSetMTU(cur, s.MTU); err != nil {
			return created, fmt.Errorf("set mtu on %s: %w", s.Name, err)
		}
	}
	if err := syncAddrs(cur, s.Addrs); err != nil {
		return created, err
	}
	if cur.Attrs().Flags&net.FlagUp == 0 {
		if err := netlink.LinkSetUp(cur); err != nil {
			return created, fmt.Errorf("bring up %s: %w", s.Name, err)
		}
	}
	return created, nil
}

func syncAddrs(l netlink.Link, want []netip.Prefix) error {
	have, err := netlink.AddrList(l, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, p := range want {
		keep[p.String()] = true
	}
	for _, a := range have {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		p, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ones, _ := a.Mask.Size()
		if !keep[netip.PrefixFrom(p.Unmap(), ones).String()] {
			netlink.AddrDel(l, &a)
		}
	}
	for _, p := range want {
		a := &netlink.Addr{IPNet: &net.IPNet{IP: toIP(p.Addr()), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}}
		if err := netlink.AddrReplace(l, a); err != nil {
			return fmt.Errorf("address %s on %s: %w", p, l.Attrs().Name, err)
		}
	}
	return nil
}

func explain(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%w (another tunnel with the same type, endpoints and key already exists)", err)
	case errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENODEV):
		return fmt.Errorf("%w (kernel module missing? try: modprobe the tunnel module)", err)
	}
	return err
}

// owned lists our interfaces keyed by name, with their alias.
func owned() (map[string]netlink.Link, error) {
	ls, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	out := map[string]netlink.Link{}
	for _, l := range ls {
		a := l.Attrs().Alias
		if strings.HasPrefix(a, aliasPersistent) || strings.HasPrefix(a, aliasBench) {
			out[l.Attrs().Name] = l
		}
	}
	return out, nil
}

func benchExpired(alias string, now time.Time) bool {
	rest, ok := strings.CutPrefix(alias, aliasBench)
	if !ok {
		return false
	}
	exp, _, _ := strings.Cut(rest, ":")
	n, err := strconv.ParseInt(exp, 10, 64)
	return err != nil || now.Unix() > n
}

// RouteSource is the default Resolver: the kernel's source address for remote.
func RouteSource(remote netip.Addr) (netip.Addr, error) {
	rs, err := netlink.RouteGet(toIP(remote))
	if err != nil {
		return netip.Addr{}, err
	}
	for _, r := range rs {
		if a, ok := netip.AddrFromSlice(r.Src); ok && a.IsValid() && !a.IsUnspecified() {
			return a.Unmap(), nil
		}
	}
	return netip.Addr{}, fmt.Errorf("kernel returned no source address")
}
