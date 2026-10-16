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
//	neotunnel-user:<hash>            user-space tunnel (NeoObfs), owned by the
//	                                 daemon process that holds its TUN device
const (
	aliasPersistent = "neotunnel:"
	aliasBench      = "neotunnel-bench:"
	aliasUser       = "neotunnel-user:"
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
	case config.KindWireGuard:
		return &netlink.Wireguard{LinkAttrs: attrs}, nil
	}
	return nil, fmt.Errorf("unsupported kind %s", s.Kind)
}

// create adds the kernel interface for s and configures what can only be
// set right after creation (WireGuard keys and peer).
func create(s LinkSpec) error {
	if s.Kind == config.KindL2TPv3 {
		return createL2TP(s)
	}
	l, err := buildLink(s)
	if err != nil {
		return err
	}
	if err := netlink.LinkAdd(l); err != nil {
		return fmt.Errorf("create %s (%s): %w", s.Name, s.Kind, explain(err))
	}
	if s.Kind == config.KindWireGuard {
		if err := wgConfigure(s); err != nil {
			netlink.LinkDel(l)
			return err
		}
	}
	return nil
}

// delLink removes one of our interfaces (L2TP sessions go with their tunnel).
func delLink(l netlink.Link) error {
	if id, ok := l2tpTunnelOf(l.Attrs().Name); ok {
		return ipCmd("l2tp", "del", "tunnel", "tunnel_id", id)
	}
	return netlink.LinkDel(l)
}

// ensure makes the kernel interface match s. alias is the full alias to set.
// Returns true if the link was (re)created.
func ensure(s LinkSpec, alias string) (bool, error) {
	cur, err := netlink.LinkByName(s.Name)
	created := false
	if err == nil && cur.Attrs().Alias != alias {
		if err := delLink(cur); err != nil {
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
		if err := create(s); err != nil {
			return false, err
		}
		if cur, err = netlink.LinkByName(s.Name); err != nil {
			return false, err
		}
		// The alias marks the link as ours and complete; set it last.
		if err := netlink.LinkSetAlias(cur, alias); err != nil {
			delLink(cur)
			return false, err
		}
		created = true
	}
	return created, configure(cur, s)
}

// configure brings an existing interface's MTU, addresses and state in line.
func configure(cur netlink.Link, s LinkSpec) error {
	if cur.Attrs().MTU != s.MTU {
		if err := netlink.LinkSetMTU(cur, s.MTU); err != nil {
			return fmt.Errorf("set mtu on %s: %w", s.Name, err)
		}
	}
	if err := syncAddrs(cur, s.Addrs); err != nil {
		return err
	}
	if cur.Attrs().Flags&net.FlagUp == 0 {
		if err := netlink.LinkSetUp(cur); err != nil {
			return fmt.Errorf("bring up %s: %w", s.Name, err)
		}
	}
	return nil
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
		if strings.HasPrefix(a, aliasPersistent) || strings.HasPrefix(a, aliasBench) || strings.HasPrefix(a, aliasUser) {
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
