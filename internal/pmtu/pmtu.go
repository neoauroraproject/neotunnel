// Package pmtu measures the path MTU to a tunnel's far end, so tunnels can be
// sized from the real path instead of assuming 1500 bytes. Results are kept
// in pmtu.json and read by the engine when it renders a tunnel.
package pmtu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"neotunnel/internal/config"
)

const (
	Max = 1500
	Min = 1280
)

type entry struct {
	MTU int   `json:"mtu"`
	At  int64 `json:"at"` // unix seconds
}

var (
	mu     sync.Mutex
	cache  map[string]entry
	loaded time.Time // mtime of the file the cache came from
)

func path() string { return filepath.Join(config.Dir, "pmtu.json") }

func load() map[string]entry {
	fi, err := os.Stat(path())
	if err != nil {
		return map[string]entry{}
	}
	if cache != nil && fi.ModTime().Equal(loaded) {
		return cache
	}
	m := map[string]entry{}
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, &m)
	}
	cache, loaded = m, fi.ModTime()
	return m
}

// Cached returns the last measured path MTU to a (0 = never measured).
func Cached(a netip.Addr) int {
	mu.Lock()
	defer mu.Unlock()
	return load()[a.String()].MTU
}

// Age returns how long ago a was measured (a large value if never).
func Age(a netip.Addr) time.Duration {
	mu.Lock()
	defer mu.Unlock()
	e, ok := load()[a.String()]
	if !ok {
		return 1 << 62
	}
	return time.Since(time.Unix(e.At, 0))
}

// Save records a measurement; it reports whether the value changed.
func Save(a netip.Addr, mtu int) bool {
	mu.Lock()
	defer mu.Unlock()
	changed := false
	config.WithLock(func() error {
		m := map[string]entry{}
		for k, v := range load() {
			m[k] = v
		}
		changed = m[a.String()].MTU != mtu
		m[a.String()] = entry{MTU: mtu, At: time.Now().Unix()}
		b, _ := json.Marshal(m)
		os.MkdirAll(config.Dir, 0o700)
		if err := os.WriteFile(path(), b, 0o600); err != nil {
			return err
		}
		cache = nil
		return nil
	})
	return changed
}

// Forget drops a measurement (when probing is turned off).
func Forget(a netip.Addr) {
	mu.Lock()
	defer mu.Unlock()
	config.WithLock(func() error {
		m := map[string]entry{}
		for k, v := range load() {
			if k != a.String() {
				m[k] = v
			}
		}
		b, _ := json.Marshal(m)
		cache = nil
		return os.WriteFile(path(), b, 0o600)
	})
}

// Pinger sends one don't-fragment probe of an IP packet of size bytes and
// reports whether a reply came back.
type Pinger func(ctx context.Context, a netip.Addr, size int) bool

// SystemPing uses iputils ping with fragmentation prohibited.
func SystemPing(ctx context.Context, a netip.Addr, size int) bool {
	hdr := 28 // IPv4 20 + ICMP 8
	args := []string{"-c", "2", "-i", "0.2", "-W", "1", "-M", "do"}
	if a.Is6() {
		hdr = 48
		args = append([]string{"-6"}, args...)
	}
	args = append(args, "-s", strconv.Itoa(size-hdr), a.String())
	return exec.CommandContext(ctx, "ping", args...).Run() == nil
}

// Probe finds the largest packet that reaches a unfragmented, between Min
// and Max, with a binary search.
func Probe(ctx context.Context, a netip.Addr, ping Pinger) (int, error) {
	if ping == nil {
		ping = SystemPing
	}
	if ping(ctx, a, Max) {
		return Max, nil
	}
	if !ping(ctx, a, Min) && !ping(ctx, a, Min) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("%s does not answer ping, so its path MTU can't be measured", a)
	}
	lo, hi := Min, Max // lo passes, hi fails
	for hi-lo > 1 {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		mid := (lo + hi) / 2
		if ping(ctx, a, mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, nil
}
