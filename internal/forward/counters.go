package forward

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Count is one direction's bytes and packets of a forward.
type Count struct {
	Bytes   uint64
	Packets uint64
}

// ReadCounters returns the per-forward named counters, keyed by their nft
// name (counterName(key) + "u" or "d"). Missing nft or table yields nil.
func ReadCounters() map[string]Count {
	out, err := exec.Command("nft", "-j", "list", "counters", "table", "inet", "neotunnel").Output()
	if err != nil {
		return nil
	}
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
				Bytes   uint64 `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil
	}
	m := map[string]Count{}
	for _, e := range doc.Nftables {
		if c := e.Counter; c != nil && strings.HasPrefix(c.Name, "nt") {
			m[c.Name] = Count{Bytes: c.Bytes, Packets: c.Packets}
		}
	}
	return m
}

// keepCounts gives the script's named counters the values they have now.
func keepCounts(script string, cur map[string]Count) string {
	if len(cur) == 0 {
		return script
	}
	var b strings.Builder
	for _, line := range strings.SplitAfter(script, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "counter "); ok && strings.HasSuffix(name, " { }") {
			name = strings.TrimSuffix(name, " { }")
			if c, ok := cur[name]; ok && !strings.Contains(name, " ") {
				fmt.Fprintf(&b, "  counter %s { packets %d bytes %d }\n", name, c.Packets, c.Bytes)
				continue
			}
		}
		b.WriteString(line)
	}
	return b.String()
}
