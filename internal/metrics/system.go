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
	prev Stat
}

// Read returns both shares in percent since the previous Read (ok is false
// on the first one, or where /proc/stat doesn't exist).
func (c *CPU) Read() (total, hottest float64, ok bool) {
	cur, ok := ReadStat()
	if !ok {
		return 0, 0, false
	}
	prev := c.prev
	c.prev = cur
	if prev == nil {
		return 0, 0, false
	}
	u, ok := Between(prev, cur)
	return u.Total, u.Hottest, ok
}

// parseStat reads the cpu lines of /proc/stat. Idle and I/O wait count as
// not busy; guest time is already counted in user time.
func parseStat(s string) Stat {
	out := Stat{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var j Jiffies
		var idle uint64
		for i, x := range f[1:] {
			if i >= 8 {
				break
			}
			v, _ := strconv.ParseUint(x, 10, 64)
			j.Total += v
			switch i {
			case 3, 4:
				idle += v
			case 6:
				j.Softirq = v
			}
		}
		j.Busy = j.Total - idle
		out[f[0]] = j
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
