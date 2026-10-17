package engine

import (
	"fmt"
	"strings"
)

// cpuMask is the sysfs mask of the first n CPUs: hex words of 32 bits,
// highest first, separated by commas.
func cpuMask(n int) string {
	var words []string
	for left := n; left > 0; left -= 32 {
		bits := min(left, 32)
		w := uint64(1)<<bits - 1
		words = append([]string{fmt.Sprintf("%08x", w)}, words...)
	}
	if len(words) == 0 {
		return "0"
	}
	return strings.Join(words, ",")
}

// sameMask compares masks ignoring leading zeros and separators.
func sameMask(a, b string) bool {
	norm := func(s string) string {
		s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
		s = strings.TrimLeft(s, "0")
		return s
	}
	return norm(a) == norm(b)
}
