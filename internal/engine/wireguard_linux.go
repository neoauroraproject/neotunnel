package engine

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// WireGuard generic netlink (include/uapi/linux/wireguard.h).
const (
	wgCmdSetDevice = 1

	wgDeviceIfname     = 2
	wgDevicePrivateKey = 3
	wgDeviceFlags      = 5
	wgDeviceListenPort = 6
	wgDevicePeers      = 8

	wgDeviceReplacePeers = 1

	wgPeerPublicKey  = 1
	wgPeerFlags      = 3
	wgPeerEndpoint   = 4
	wgPeerKeepalive  = 5
	wgPeerAllowedIPs = 9

	wgPeerReplaceAllowedIPs = 2

	wgAllowedIPFamily = 1
	wgAllowedIPAddr   = 2
	wgAllowedIPMask   = 3
)

// wgConfigure sets a WireGuard interface's key, port and its single peer.
// The peer may send from and to any address (allowed IPs 0/0, ::/0): the
// tunnel is point to point and forwarded clients keep their own addresses.
func wgConfigure(s LinkSpec) error {
	err := wgConfigureVariant(s, "")
	if err != nil && os.Getenv("NEOTUNNEL_WG_BISECT") != "" {
		for _, v := range []string{"noDevFlags", "noPeerFlags", "noKeepalive", "noAllowed", "noEndpoint", "noPort", "v4only", "nonNestedFlag", "cidr32"} {
			e := wgConfigureVariant(s, v)
			fmt.Fprintf(os.Stderr, "WGBISECT %s: %v\n", v, e)
		}
	}
	return err
}

func wgConfigureVariant(s LinkSpec, v string) error {
	priv, peer, err := wgKeys(s)
	if err != nil {
		return err
	}
	fam, err := netlink.GenlFamilyGet("wireguard")
	if err != nil {
		return fmt.Errorf("wireguard is not available in this kernel: %w", err)
	}
	req := nl.NewNetlinkRequest(int(fam.ID), unix.NLM_F_ACK)
	req.AddData(&nl.Genlmsg{Command: wgCmdSetDevice, Version: 1})
	req.AddData(nl.NewRtAttr(wgDeviceIfname, nl.ZeroTerminated(s.Name)))
	req.AddData(nl.NewRtAttr(wgDevicePrivateKey, priv[:]))
	if v != "noPort" {
		req.AddData(nl.NewRtAttr(wgDeviceListenPort, nl.Uint16Attr(s.Port)))
	}
	if v != "noDevFlags" {
		req.AddData(nl.NewRtAttr(wgDeviceFlags, nl.Uint32Attr(wgDeviceReplacePeers)))
	}

	peers := nl.NewRtAttr(wgDevicePeers|unix.NLA_F_NESTED, nil)
	p := peers.AddRtAttr(unix.NLA_F_NESTED, nil)
	p.AddRtAttr(wgPeerPublicKey, peer[:])
	if v != "noPeerFlags" {
		p.AddRtAttr(wgPeerFlags, nl.Uint32Attr(wgPeerReplaceAllowedIPs))
	}
	if v != "noEndpoint" {
		p.AddRtAttr(wgPeerEndpoint, sockaddr(s))
	}
	ka := s.Keepalive
	if ka == 0 {
		ka = 25
	} else if ka < 0 {
		ka = 0
	}
	if v != "noKeepalive" {
		p.AddRtAttr(wgPeerKeepalive, nl.Uint16Attr(uint16(ka)))
	}
	ips := p.AddRtAttr(wgPeerAllowedIPs|unix.NLA_F_NESTED, nil)
	if v == "noAllowed" {
		ips = nl.NewRtAttr(unix.NLA_F_NESTED, nil)
	}
	for i, fam := range []struct {
		af   uint16
		addr []byte
	}{{unix.AF_INET, make([]byte, 4)}, {unix.AF_INET6, make([]byte, 16)}} {
		if v == "v4only" && i == 1 {
			continue
		}
		flag := unix.NLA_F_NESTED
		if v == "nonNestedFlag" {
			flag = 0
		}
		a := ips.AddRtAttr(flag, nil)
		a.AddRtAttr(wgAllowedIPFamily, nl.Uint16Attr(fam.af))
		a.AddRtAttr(wgAllowedIPAddr, fam.addr)
		mask := byte(0)
		if v == "cidr32" {
			mask = 32
		}
		a.AddRtAttr(wgAllowedIPMask, []byte{mask})
	}
	req.AddData(peers)
	if _, err := req.Execute(unix.NETLINK_GENERIC, 0); err != nil {
		return fmt.Errorf("configure wireguard %s: %w", s.Name, err)
	}
	return nil
}

// sockaddr encodes the far end as a struct sockaddr_in / sockaddr_in6.
func sockaddr(s LinkSpec) []byte {
	if s.Remote.Is4() {
		b := make([]byte, 16)
		binary.NativeEndian.PutUint16(b[0:], unix.AF_INET)
		binary.BigEndian.PutUint16(b[2:], s.Port)
		a := s.Remote.As4()
		copy(b[4:], a[:])
		return b
	}
	b := make([]byte, 28)
	binary.NativeEndian.PutUint16(b[0:], unix.AF_INET6)
	binary.BigEndian.PutUint16(b[2:], s.Port)
	a := s.Remote.As16()
	copy(b[8:], a[:])
	return b
}
