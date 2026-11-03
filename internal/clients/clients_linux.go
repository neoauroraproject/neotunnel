package clients

import (
	"net"
	"net/netip"
	"os"
	"os/exec"
	"time"

	"github.com/vishvananda/netlink"
)

// Of reports who uses each of ports (by key), from one read of the
// connection table and of the day's record.
func Of(ports map[string]Port) map[string]Report {
	bytes, times := account()
	flows := openFlows()
	day := dayRecord()
	out := map[string]Report{}
	for k, p := range ports {
		r := build(p, flows, day)
		r.Bytes, r.Times = bytes, times
		out[k] = r
	}
	return out
}

// account has the kernel count each new connection's bytes and note when
// it opened, for the report (a counter and a time per connection). It
// says whether both are on, which connections opened before they were
// don't show.
func account() (bytes, times bool) {
	set := func(name string) bool {
		p := "/proc/sys/net/netfilter/" + name
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 && b[0] == '1' {
			return true
		}
		return os.WriteFile(p, []byte("1"), 0o644) == nil
	}
	return set("nf_conntrack_acct"), set("nf_conntrack_timestamp")
}

// openFlows lists the connections clients opened to this server.
func openFlows() []flow {
	local := localAddrs()
	var out []flow
	for _, fam := range []netlink.InetFamily{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		fs, err := netlink.ConntrackTableList(netlink.ConntrackTable, fam)
		if err != nil {
			continue
		}
		for _, f := range fs {
			dst, ok1 := netip.AddrFromSlice(f.Forward.DstIP)
			src, ok2 := netip.AddrFromSlice(f.Forward.SrcIP)
			if !ok1 || !ok2 || !local[dst.Unmap()] || closed(f) {
				continue
			}
			fl := flow{proto: protoName(int(f.Forward.Protocol)), client: src.Unmap(), port: int(f.Forward.DstPort), tx: f.Forward.Bytes, rx: f.Reverse.Bytes}
			if f.TimeStart > 0 {
				fl.start = int64(f.TimeStart / uint64(time.Second))
			}
			out = append(out, fl)
		}
	}
	return out
}

// closed is a TCP connection already finished, kept for its last packets.
func closed(f *netlink.ConntrackFlow) bool {
	if f.Forward.Protocol != 6 || f.ProtoInfo == nil {
		return false
	}
	t, ok := f.ProtoInfo.(*netlink.ProtoInfoTCP)
	// TIME_WAIT and CLOSE (nf_conntrack_tcp.h).
	return ok && (t.State == 7 || t.State == 8)
}

// localAddrs are this server's own addresses: connections to them are
// clients'; connections this server opens elsewhere are not.
func localAddrs() map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	as, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				out[ip.Unmap()] = true
			}
		}
	}
	return out
}

// dayRecord reads the record forward.SeenScript keeps.
func dayRecord() []seen {
	var out []seen
	now := time.Now().Unix()
	for _, set := range []string{"c4", "c6"} {
		b, err := exec.Command("nft", "-j", "list", "set", "inet", "neotunnel_seen", set).Output()
		if err != nil {
			continue
		}
		out = append(out, parseSeen(b, now)...)
	}
	return out
}
