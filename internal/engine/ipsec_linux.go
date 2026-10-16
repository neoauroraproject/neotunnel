package engine

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

// policyPriority marks NeoTunnel's IPsec policies.
const policyPriority = 0x4e54

// rekeyAt: an outbound SA is replaced before its 32-bit sequence number
// runs out (the replacement gets a fresh random IV salt in the kernel).
const rekeyAt = 0xf0000000

func hostNet(a netip.Addr) *net.IPNet {
	return &net.IPNet{IP: toIP(a), Mask: net.CIDRMask(a.BitLen(), a.BitLen())}
}

type saKey struct {
	dst netip.Addr
	spi int
}

func stateFor(src, dst netip.Addr, sa espSA) *netlink.XfrmState {
	return &netlink.XfrmState{
		Src: toIP(src), Dst: toIP(dst), Proto: netlink.XFRM_PROTO_ESP, Mode: netlink.XFRM_MODE_TRANSPORT,
		Spi: int(sa.SPI), Reqid: int(sa.SPI),
		// No replay window: keys are static, so a rebooted peer starts its
		// sequence again; the tunnels' payload has its own protection.
		ReplayWindow: 0,
		Aead:         &netlink.XfrmStateAlgo{Name: sa.Algo, Key: sa.Key, ICVLen: 128},
	}
}

// syncIPsec installs the SAs and outbound policies of encrypted tunnels and
// removes ours that are no longer wanted.
func syncIPsec(specs []LinkSpec) []error {
	var errs []error
	wantSA := map[saKey]*netlink.XfrmState{}
	var wantPol []*netlink.XfrmPolicy
	for _, s := range specs {
		if s.Encrypt == "" {
			continue
		}
		out, err1 := espFor(s, s.Side)
		in, err2 := espFor(s, 1-s.Side)
		if err1 != nil || err2 != nil {
			errs = append(errs, fmt.Errorf("tunnel %s: encryption keys: %v", s.Tunnel, firstErr(err1, err2)))
			continue
		}
		wantSA[saKey{s.Remote, int(out.SPI)}] = stateFor(s.Local, s.Remote, out)
		wantSA[saKey{s.Local, int(in.SPI)}] = stateFor(s.Remote, s.Local, in)
		proto, port := outerProto(s)
		wantPol = append(wantPol, &netlink.XfrmPolicy{
			Src: hostNet(s.Local), Dst: hostNet(s.Remote), Proto: netlink.Proto(proto), DstPort: int(port),
			Dir: netlink.XFRM_DIR_OUT, Priority: policyPriority,
			Tmpls: []netlink.XfrmPolicyTmpl{{
				Src: toIP(s.Local), Dst: toIP(s.Remote), Proto: netlink.XFRM_PROTO_ESP,
				Mode: netlink.XFRM_MODE_TRANSPORT, Reqid: int(out.SPI),
			}},
		})
	}

	// Policies first, so traffic never goes out with a policy and no SA.
	pols, err := netlink.XfrmPolicyList(netlink.FAMILY_ALL)
	if err != nil {
		if len(wantPol) == 0 {
			return errs // no IPsec support and none needed
		}
		return append(errs, fmt.Errorf("IPsec is not available (xfrm_user module?): %w", err))
	}
	for _, p := range pols {
		if p.Priority != policyPriority || p.Dir != netlink.XFRM_DIR_OUT {
			continue
		}
		if !hasPolicy(wantPol, p) {
			netlink.XfrmPolicyDel(&p)
		}
	}
	states, _ := netlink.XfrmStateList(netlink.FAMILY_ALL)
	have := map[saKey]netlink.XfrmState{}
	for _, st := range states {
		if st.Proto != netlink.XFRM_PROTO_ESP || uint32(st.Spi)&0xff000000 != spiBase {
			continue
		}
		dst, _ := netip.AddrFromSlice(st.Dst)
		k := saKey{dst.Unmap(), st.Spi}
		if _, ok := wantSA[k]; !ok {
			netlink.XfrmStateDel(&st)
			continue
		}
		have[k] = st
	}
	for k, st := range wantSA {
		if cur, ok := have[k]; ok {
			if cur.Replay == nil || cur.Replay.OSeq < rekeyAt {
				continue
			}
			netlink.XfrmStateDel(&cur)
		}
		if err := netlink.XfrmStateAdd(st); err != nil {
			errs = append(errs, fmt.Errorf("IPsec SA to %s: %w (esp4/esp6 kernel modules?)", k.dst, err))
		}
	}
	for _, p := range wantPol {
		if err := netlink.XfrmPolicyUpdate(p); err != nil {
			errs = append(errs, fmt.Errorf("IPsec policy to %s: %w", p.Dst.IP, err))
		}
	}
	return errs
}

func hasPolicy(want []*netlink.XfrmPolicy, p netlink.XfrmPolicy) bool {
	for _, w := range want {
		if w.Src.String() == p.Src.String() && w.Dst.String() == p.Dst.String() &&
			w.Proto == p.Proto && w.DstPort == p.DstPort {
			return true
		}
	}
	return false
}

// removeIPsec deletes every SA and policy NeoTunnel installed.
func removeIPsec() { syncIPsec(nil) }

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
