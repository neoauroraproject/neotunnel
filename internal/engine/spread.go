package engine

// Pure helpers of spread_linux.go (CPU lists and masks, interrupt names),
// kept apart so they are tested on every platform.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var trailingNum = regexp.MustCompile(`(\d+)$`)

func rxIRQName(name string, devs []string) bool {
	match := false
	for _, d := range devs {
		if d != "" && (name == d || strings.HasPrefix(name, d+"-") || strings.HasPrefix(name, d+"_")) {
			match = true
		}
	}
	if !match {
		return false
	}
	l := strings.ToLower(name)
	for _, skip := range []string{"output", "event", "config", "control", "ctrl", "mgmt", "async"} {
		if strings.Contains(l, skip) {
			return false
		}
	}
	return !(strings.Contains(l, "tx") && !strings.Contains(l, "rx"))
}

// parseCPUList reads "0-3,5".
func parseCPUList(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		for c := a; c <= b && c-a < 4096; c++ {
			out = append(out, c)
		}
	}
	return out
}

// spreadMask is the RPS mask for a queue whose interrupt runs on irqCPUs:
// every core but those, so the interrupt's core keeps only the driver's
// work. With nothing left (one core, or an interrupt on every core) it is
// every core; with one core, none.
func spreadMask(ncpu int, irqCPUs []int) string {
	if ncpu <= 1 {
		return "0"
	}
	skip := map[int]bool{}
	for _, c := range irqCPUs {
		skip[c] = true
	}
	var cpus []int
	for c := range ncpu {
		if !skip[c] {
			cpus = append(cpus, c)
		}
	}
	if len(cpus) == 0 {
		return cpuMask(ncpu)
	}
	return maskOf(cpus)
}

// maskOf is the sysfs mask of the given cores.
func maskOf(cpus []int) string {
	top := 0
	for _, c := range cpus {
		top = max(top, c)
	}
	words := make([]uint32, top/32+1)
	for _, c := range cpus {
		words[c/32] |= 1 << (c % 32)
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[len(words)-1-i] = fmt.Sprintf("%08x", w)
	}
	return strings.Join(parts, ",")
}
