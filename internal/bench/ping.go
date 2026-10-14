package bench

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"neotunnel/internal/probe"
)

var (
	pingCounts = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
	pingRTT    = regexp.MustCompile(`= ([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+) ms`)
)

// PingICMP measures latency with the system ping, for far ends that don't
// run neotunnel (e.g. a MikroTik). Jitter is ping's mean deviation.
func PingICMP(ctx context.Context, addr string, count int) (probe.LatencyResult, error) {
	out, _ := exec.CommandContext(ctx, "ping", "-c", strconv.Itoa(count), "-i", "0.2", "-W", "2", "-q", addr).CombinedOutput()
	r, err := ParsePing(string(out))
	if err != nil {
		return r, fmt.Errorf("ping %s: %w", addr, err)
	}
	return r, nil
}

// ParsePing reads iputils ping summary output.
func ParsePing(out string) (probe.LatencyResult, error) {
	var r probe.LatencyResult
	m := pingCounts.FindStringSubmatch(out)
	if m == nil {
		return r, fmt.Errorf("unexpected output")
	}
	r.Sent, _ = strconv.Atoi(m[1])
	r.Received, _ = strconv.Atoi(m[2])
	if r.Received == 0 {
		return r, fmt.Errorf("no replies")
	}
	if t := pingRTT.FindStringSubmatch(out); t != nil {
		d := func(s string) time.Duration {
			f, _ := strconv.ParseFloat(s, 64)
			return time.Duration(f * float64(time.Millisecond))
		}
		r.Min, r.Avg, r.Max, r.Jitter = d(t[1]), d(t[2]), d(t[3]), d(t[4])
	}
	return r, nil
}
