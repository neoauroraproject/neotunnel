package metrics

import (
	"os"
	"strconv"
	"strings"
)

// CPU measures how busy the processor was between two reads of
// /proc/stat: all cores together, and the busiest single core. A tunnel's
// packets are often handled on one core, so that core can be full while
// the total looks calm; the busiest core shows it.
type CPU struct {
	prev map[string][2]uint64 // per line: busy and total jiffies
}

// Read returns both shares in percent since the previous Read (ok is false
// on the first one, or where /proc/stat doesn't exist).
func (c *CPU) Read() (total, hottest float64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	cur := parseStat(string(b))
	prev := c.prev
	c.prev = cur
	if prev == nil {
		return 0, 0, false
	}
	share := func(name string) (float64, bool) {
		p, ok1 := prev[name]
		n, ok2 := cur[name]
		if !ok1 || !ok2 || n[1] <= p[1] || n[0] < p[0] {
			return 0, false
		}
		return 100 * float64(n[0]-p[0]) / float64(n[1]-p[1]), true
	}
	total, ok = share("cpu")
	for name := range cur {
		if name != "cpu" {
			if v, good := share(name); good && v > hottest {
				hottest = v
			}
		}
	}
	return total, hottest, ok
}

// parseStat reads the cpu lines of /proc/stat into busy and total jiffies.
// Idle and I/O wait count as not busy.
func parseStat(s string) map[string][2]uint64 {
	out := map[string][2]uint64{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var total, idle uint64
		for i, x := range f[1:] {
			v, _ := strconv.ParseUint(x, 10, 64)
			if i >= 8 { // guest time is already counted in user time
				break
			}
			total += v
			if i == 3 || i == 4 {
				idle += v
			}
		}
		out[f[0]] = [2]uint64{total - idle, total}
	}
	return out
}

// Memory returns used memory and used swap in percent of their totals.
func Memory() (mem, swap float64, ok bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	v := map[string]float64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			n, _ := strconv.ParseFloat(f[1], 64)
			v[strings.TrimSuffix(f[0], ":")] = n
		}
	}
	if v["MemTotal"] <= 0 {
		return 0, 0, false
	}
	mem = 100 * (v["MemTotal"] - v["MemAvailable"]) / v["MemTotal"]
	if v["SwapTotal"] > 0 {
		swap = 100 * (v["SwapTotal"] - v["SwapFree"]) / v["SwapTotal"]
	}
	return mem, swap, true
}

// Load returns the one-minute load average.
func Load() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil
}
