package engine

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// L2TPv3 static (unmanaged) tunnels are configured with iproute2's
// "ip l2tp": the kernel's l2tp netlink family is not covered by the netlink
// library. Both ends use the same tunnel and session ids, derived from the
// tunnel key, which is how MikroTik and Linux pair static L2TPv3 too.

func l2tpIDs(s LinkSpec) (tunnel, session uint32) {
	t := s.Key & 0x7fffffff
	if t == 0 {
		t = 1
	}
	return t, t
}

func ipCmd(args ...string) error {
	var out bytes.Buffer
	cmd := exec.Command("ip", args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

func createL2TP(s LinkSpec) error {
	tid, sid := l2tpIDs(s)
	t, se := strconv.FormatUint(uint64(tid), 10), strconv.FormatUint(uint64(sid), 10)
	args := []string{"l2tp", "add", "tunnel", "tunnel_id", t, "peer_tunnel_id", t,
		"local", s.Local.String(), "remote", s.Remote.String()}
	if s.Port != 0 {
		p := strconv.Itoa(int(s.Port))
		args = append(args, "encap", "udp", "udp_sport", p, "udp_dport", p)
	} else {
		args = append(args, "encap", "ip")
	}
	// A leftover tunnel with our id (e.g. after a crash) would block creation.
	ipCmd("l2tp", "del", "tunnel", "tunnel_id", t)
	if err := ipCmd(args...); err != nil {
		return fmt.Errorf("%w (is the l2tp_eth kernel module available?)", err)
	}
	if err := ipCmd("l2tp", "add", "session", "name", s.Name, "tunnel_id", t,
		"session_id", se, "peer_session_id", se); err != nil {
		ipCmd("l2tp", "del", "tunnel", "tunnel_id", t)
		return err
	}
	return nil
}

var (
	l2tpSessionRe = regexp.MustCompile(`(?m)^Session \d+ in tunnel (\d+)`)
	l2tpIfnameRe  = regexp.MustCompile(`interface name: (\S+)`)
)

// l2tpTunnelOf returns the L2TP tunnel id behind an interface, if any.
func l2tpTunnelOf(ifname string) (string, bool) {
	out, err := exec.Command("ip", "l2tp", "show", "session").Output()
	if err != nil {
		return "", false
	}
	blocks := l2tpSessionRe.FindAllSubmatchIndex(out, -1)
	for i, m := range blocks {
		end := len(out)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		if n := l2tpIfnameRe.FindSubmatch(out[m[0]:end]); n != nil && string(n[1]) == ifname {
			return string(out[m[2]:m[3]]), true
		}
	}
	return "", false
}
