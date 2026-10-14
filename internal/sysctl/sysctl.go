// Package sysctl applies kernel settings NeoTunnel depends on or recommends.
package sysctl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var root = "/proc/sys"

// Required for routing traffic between the public side and tunnels.
var forwarding = map[string]string{
	"net.ipv4.ip_forward":             "1",
	"net.ipv6.conf.all.forwarding":    "1",
	"net.ipv4.conf.all.rp_filter":     "0",
	"net.ipv4.conf.default.rp_filter": "0",
}

// Optimize is the throughput/latency profile: BBR with fq, larger socket
// buffers and a busier backlog. Safe on any modern Ubuntu kernel.
var Optimize = map[string]string{
	"net.core.default_qdisc":             "fq",
	"net.ipv4.tcp_congestion_control":    "bbr",
	"net.core.rmem_max":                  "67108864",
	"net.core.wmem_max":                  "67108864",
	"net.ipv4.tcp_rmem":                  "4096 131072 67108864",
	"net.ipv4.tcp_wmem":                  "4096 65536 67108864",
	"net.core.netdev_max_backlog":        "16384",
	"net.ipv4.tcp_mtu_probing":           "1",
	"net.ipv4.tcp_fastopen":              "3",
	"net.ipv4.tcp_slow_start_after_idle": "0",
}

func path(key string) string {
	return filepath.Join(root, strings.ReplaceAll(key, ".", "/"))
}

func Get(key string) (string, error) {
	b, err := os.ReadFile(path(key))
	return strings.TrimSpace(string(b)), err
}

func set(values map[string]string) error {
	var failed []string
	for k, v := range values {
		if cur, err := Get(k); err == nil && strings.Join(strings.Fields(cur), " ") == v {
			continue
		}
		if err := os.WriteFile(path(k), []byte(v), 0o644); err != nil {
			failed = append(failed, k)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("could not set %s", strings.Join(failed, ", "))
	}
	return nil
}

func EnsureForwarding() error { return set(forwarding) }

// ApplyOptimize sets the profile now and persists it across reboots.
func ApplyOptimize() error {
	all := map[string]string{}
	for k, v := range forwarding {
		all[k] = v
	}
	for k, v := range Optimize {
		all[k] = v
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Managed by neotunnel (neotunnel optimize)\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, all[k])
	}
	if err := os.WriteFile("/etc/sysctl.d/90-neotunnel.conf", []byte(b.String()), 0o644); err != nil {
		return err
	}
	return set(all)
}
