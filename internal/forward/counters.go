package forward

import (
	"encoding/json"
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
