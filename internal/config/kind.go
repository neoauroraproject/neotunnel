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
)

// KindInfo describes what a tunnel kind carries and costs.
type KindInfo struct {
	Kind     Kind
	Layer    int  // 2 or 3
	Under6   bool // needs an IPv6 underlay (false = IPv4, or either for UDP kinds)
	UnderAny bool // works over IPv4 or IPv6
	Carry4   bool
	Carry6   bool
	// Overhead in bytes for an IPv4 underlay; UDP kinds add 20 more over IPv6.
	Overhead int
	Title    string
	// IPProto is the outer IP protocol; kinds with one can run inside UDP on
	// a chosen port (FOU), which also gets them past protocol filtering.
	IPProto int
	// UDP kinds always run on a port.
	UDP bool
}

var kinds = []KindInfo{
	{KindSIT, 3, false, false, false, true, 20, "SIT / 6to4 (IPv6 in IPv4)", 41, false},
	{KindIPIP, 3, false, false, true, false, 20, "IPIP (IPv4 in IPv4)", 4, false},
	{KindGRE, 3, false, false, true, true, 24, "GRE", 47, false},
	{KindGRETAP, 2, false, false, true, true, 38, "GRETAP (Ethernet over GRE)", 47, false},
	{KindIP6GRE, 3, true, false, true, true, 44, "GRE over IPv6", 0, false},
	{KindIP6GRETAP, 2, true, false, true, true, 58, "GRETAP over IPv6", 0, false},
	{KindIPIP6, 3, true, false, true, false, 40, "IPIP6 (IPv4 in IPv6)", 0, false},
	{KindIP6IP6, 3, true, false, false, true, 40, "IP6IP6 (IPv6 in IPv6)", 0, false},
	{KindVXLAN, 2, false, true, true, true, 50, "VXLAN", 0, true},
	{KindGENEVE, 2, false, true, true, true, 50, "GENEVE", 0, true},
}

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
func (ki KindInfo) CanUsePort() bool { return ki.UDP || ki.IPProto != 0 }

// OverheadFor returns the encapsulation cost given the underlay family and
// whether the tunnel is wrapped in UDP (FOU).
func (ki KindInfo) OverheadFor(under6, fou bool) int {
	o := ki.Overhead
	if ki.Kind == KindGRE || ki.Kind == KindGRETAP || ki.Kind == KindIP6GRE || ki.Kind == KindIP6GRETAP {
		o += 4 // key field; we always set a key
	}
	if ki.UnderAny && under6 {
		o += 20
	}
	if fou {
		o += 8
	}
	return o
}
