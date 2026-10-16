package config

import "fmt"

type Kind string

const (
	KindSIT       Kind = "sit"       // IPv6 over IPv4 (the "6to4" carrier)
	KindIPIP      Kind = "ipip"      // IPv4 over IPv4
	KindGRE       Kind = "gre"       // L3 GRE over IPv4
	KindGRETAP    Kind = "gretap"    // L2 GRE over IPv4
	KindIP6GRE    Kind = "ip6gre"    // L3 GRE over IPv6
	KindIP6GRETAP Kind = "ip6gretap" // L2 GRE over IPv6
	KindIPIP6     Kind = "ipip6"     // IPv4 over IPv6
	KindIP6IP6    Kind = "ip6ip6"    // IPv6 over IPv6
	KindVXLAN     Kind = "vxlan"     // L2 over UDP
	KindGENEVE    Kind = "geneve"    // L2 over UDP
	KindL2TPv3    Kind = "l2tpv3"    // L2 pseudowire over IP (proto 115) or UDP
	KindWireGuard Kind = "wireguard" // L3, encrypted, over UDP (kernel)
	KindObfs      Kind = "obfs"      // L3, encrypted and obfuscated UDP (user space)
)

// KindInfo describes what a tunnel kind carries and costs.
type KindInfo struct {
	Kind     Kind
	Title    string
	Layer    int  // 2 or 3
	Under6   bool // needs an IPv6 underlay (false = IPv4, or either when UnderAny)
	UnderAny bool // works over IPv4 or IPv6
	Carry4   bool
	Carry6   bool
	// Overhead in bytes for an IPv4 underlay without a port; see OverheadFor.
	Overhead int
	// IPProto is the outer IP protocol (0 for kinds that always use UDP).
	IPProto int
	// FOU: the kind can be wrapped in UDP on a chosen port, which also gets
	// it past protocol filtering.
	FOU bool
	// UDP kinds always run on a port; PortEncap kinds switch to their own
	// UDP encapsulation when a port is set (L2TPv3).
	UDP       bool
	PortEncap bool
	// Encrypted kinds protect their traffic themselves.
	Encrypted bool
	// Userspace kinds are served by the daemon's own data plane.
	Userspace bool
	// Managed kinds need NeoTunnel on both ends (keys are derived from the
	// tunnel's shared secret).
	NeedsPeer bool
}

var kinds = []KindInfo{
	{Kind: KindSIT, Title: "SIT / 6to4 (IPv6 in IPv4)", Layer: 3, Carry6: true, Overhead: 20, IPProto: 41, FOU: true},
	{Kind: KindIPIP, Title: "IPIP (IPv4 in IPv4)", Layer: 3, Carry4: true, Overhead: 20, IPProto: 4, FOU: true},
	{Kind: KindGRE, Title: "GRE", Layer: 3, Carry4: true, Carry6: true, Overhead: 24, IPProto: 47, FOU: true},
	{Kind: KindGRETAP, Title: "GRETAP (Ethernet over GRE)", Layer: 2, Carry4: true, Carry6: true, Overhead: 38, IPProto: 47, FOU: true},
	{Kind: KindIP6GRE, Title: "GRE over IPv6", Layer: 3, Under6: true, Carry4: true, Carry6: true, Overhead: 44, IPProto: 47},
	{Kind: KindIP6GRETAP, Title: "GRETAP over IPv6", Layer: 2, Under6: true, Carry4: true, Carry6: true, Overhead: 58, IPProto: 47},
	{Kind: KindIPIP6, Title: "IPIP6 (IPv4 in IPv6)", Layer: 3, Under6: true, Carry4: true, Overhead: 40, IPProto: 4},
	{Kind: KindIP6IP6, Title: "IP6IP6 (IPv6 in IPv6)", Layer: 3, Under6: true, Carry6: true, Overhead: 40, IPProto: 41},
	{Kind: KindVXLAN, Title: "VXLAN", Layer: 2, UnderAny: true, Carry4: true, Carry6: true, Overhead: 50, UDP: true},
	{Kind: KindGENEVE, Title: "GENEVE", Layer: 2, UnderAny: true, Carry4: true, Carry6: true, Overhead: 50, UDP: true},
	{Kind: KindL2TPv3, Title: "L2TPv3 (Ethernet pseudowire)", Layer: 2, UnderAny: true, Carry4: true, Carry6: true, Overhead: 38, IPProto: 115, PortEncap: true},
	{Kind: KindWireGuard, Title: "WireGuard", Layer: 3, UnderAny: true, Carry4: true, Carry6: true, Overhead: 60, UDP: true, Encrypted: true, NeedsPeer: false},
	{Kind: KindObfs, Title: "NeoObfs (encrypted, obfuscated UDP)", Layer: 3, UnderAny: true, Carry4: true, Carry6: true, Overhead: ObfsOverhead, UDP: true, Encrypted: true, Userspace: true, NeedsPeer: true},
}

// ObfsOverhead is NeoObfs' per-packet cost over IPv4 without padding:
// IP 20 + UDP 8 + nonce 24 + tag 16 + header 14.
const ObfsOverhead = 82

func Kinds() []KindInfo { return kinds }

func LookupKind(k Kind) (KindInfo, error) {
	for _, ki := range kinds {
		if ki.Kind == k {
			return ki, nil
		}
	}
	return KindInfo{}, fmt.Errorf("unknown tunnel kind %q", k)
}

// CanUsePort reports whether the kind can run on a chosen UDP port.
func (ki KindInfo) CanUsePort() bool { return ki.UDP || ki.FOU || ki.PortEncap }

// IsGRE reports whether the kind carries a GRE key field.
func (ki KindInfo) IsGRE() bool {
	switch ki.Kind {
	case KindGRE, KindGRETAP, KindIP6GRE, KindIP6GRETAP:
		return true
	}
	return false
}

// OverheadFor returns the encapsulation cost given the underlay family and
// whether a port is in use (FOU for IP-protocol kinds, UDP encap for L2TPv3).
func (ki KindInfo) OverheadFor(under6, port bool) int {
	o := ki.Overhead
	if ki.IsGRE() {
		o += 4 // key field; we always set a key
	}
	if ki.UnderAny && under6 {
		o += 20
	}
	if port && ki.FOU {
		o += 8
	}
	if port && ki.PortEncap {
		o += 12 // UDP header + L2TPv3 UDP header
	}
	return o
}
