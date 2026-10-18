// Package sysinfo reports facts about this host's hardware.
package sysinfo

import (
	"os"
	"strings"
	"sync"
)

var (
	aesOnce sync.Once
	aesNI   bool
)

// AESNI reports whether the CPU has AES instructions (x86 "aes", ARM "aes"
// in /proc/cpuinfo). With them AES-GCM is the cheapest IPsec cipher;
// without, ChaCha20-Poly1305 is.
func AESNI() bool {
	aesOnce.Do(func() {
		b, err := os.ReadFile("/proc/cpuinfo")
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			if k = strings.TrimSpace(k); k != "flags" && k != "Features" {
				continue
			}
			for _, f := range strings.Fields(v) {
				if f == "aes" {
					aesNI = true
					return
				}
			}
			return
		}
	})
	return aesNI
}
