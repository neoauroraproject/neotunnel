package forward

import "fmt"

// A forward that is removed, or moved to another tunnel or target, leaves
// its open connections in the kernel's connection tracking, still
// translated to the old target: a UDP client that keeps sending (or a TCP
// connection that stays open) goes on through the old tunnel as if
// nothing changed. Apply forgets those connections once the new rules are
// in, so they start over on the new path.

// flowTargets maps "tcp/443" to where the rules send that port.
func flowTargets(rs Ruleset) map[string]string {
	out := map[string]string{}
	for _, r := range rs.Rules {
		where := fmt.Sprintf("%s>%s:%d", r.Iface, r.Target, r.To)
		for _, p := range r.Proto {
			for _, port := range Expand(r.Ports) {
				out[fmt.Sprintf("%s/%d", p, port)] = where
			}
		}
	}
	return out
}

// changedFlows are the ports of before that now go elsewhere or nowhere.
func changedFlows(before, now map[string]string) map[string]bool {
	out := map[string]bool{}
	for k, v := range before {
		if now[k] != v {
			out[k] = true
		}
	}
	return out
}
