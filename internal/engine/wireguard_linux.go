package engine

import (
	"encoding/binary"
	"fmt"

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
	priv, peer, err := wgKeys(s)
	if err != nil {
		return err
	}
	fam, err := netlink.GenlFamilyGet("wireguard")
	if err != nil {
		return fmt.Errorf("wireguard is not available in this kernel: %w", err)
	}
	req := nl.NewNetlinkRequest(int(fam.ID), unix.NLM_F_ACK)
	req.AddData(genlHeader{cmd: wgCmdSetDevice, version: 1})
	req.AddData(nl.NewRtAttr(wgDeviceIfname, nl.ZeroTerminated(s.Name)))
	req.AddData(nl.NewRtAttr(wgDevicePrivateKey, priv[:]))
	req.AddData(nl.NewRtAttr(wgDeviceListenPort, nl.Uint16Attr(s.Port)))
	req.AddData(nl.NewRtAttr(wgDeviceFlags, nl.Uint32Attr(wgDeviceReplacePeers)))

	peers := nl.NewRtAttr(wgDevicePeers|unix.NLA_F_NESTED, nil)
	p := peers.AddRtAttr(unix.NLA_F_NESTED, nil)
	p.AddRtAttr(wgPeerPublicKey, peer[:])
	p.AddRtAttr(wgPeerFlags, nl.Uint32Attr(wgPeerReplaceAllowedIPs))
	p.AddRtAttr(wgPeerEndpoint, sockaddr(s))
	ka := s.Keepalive
	if ka == 0 {
		ka = 25
	} else if ka < 0 {
		ka = 0
	}
	p.AddRtAttr(wgPeerKeepalive, nl.Uint16Attr(uint16(ka)))
	ips := p.AddRtAttr(wgPeerAllowedIPs|unix.NLA_F_NESTED, nil)
	for _, fam := range []struct {
		af   uint16
		addr []byte
	}{{unix.AF_INET, make([]byte, 4)}, {unix.AF_INET6, make([]byte, 16)}} {
		a := ips.AddRtAttr(unix.NLA_F_NESTED, nil)
		a.AddRtAttr(wgAllowedIPFamily, nl.Uint16Attr(fam.af))
		a.AddRtAttr(wgAllowedIPAddr, fam.addr)
		a.AddRtAttr(wgAllowedIPMask, []byte{0})
	}
	req.AddData(peers)
	if _, err := req.Execute(unix.NETLINK_GENERIC, 0); err != nil {
		return fmt.Errorf("configure wireguard %s: %w", s.Name, err)
	}
	return nil
}

// genlHeader is the generic netlink header: command, version and two
// reserved bytes that must be zero. netlink's nl.Genlmsg serializes four
// bytes from a two-byte struct, so its reserved bytes are whatever memory
// follows; kernels that validate the header strictly (7.0's WireGuard)
// then reject the request with EINVAL, now and then.
type genlHeader struct{ cmd, version uint8 }

func (h genlHeader) Len() int          { return nl.SizeofGenlmsg }
func (h genlHeader) Serialize() []byte { return []byte{h.cmd, h.version, 0, 0} }

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
