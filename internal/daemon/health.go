package daemon

import (
	"context"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/health"
)

const healthEvery = 8 * time.Second

var pingTime = regexp.MustCompile(`time=([\d.]+)`)

// pingOnce sends a single ICMP echo to addr through the tunnel and returns
// the round-trip time. The daemon runs as root, so system ping is fine.
func pingOnce(ctx context.Context, addr string) (time.Duration, bool) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, false
	}
	bin := "ping"
	args := []string{"-c", "1", "-W", "2", "-n", "-q"}
	if a.Is6() {
		args = append([]string{"-6"}, args...)
	}
	out, err := exec.CommandContext(cctx, bin, append(args, addr)...).Output()
	if err != nil {
		return 0, false
	}
	if m := pingTime.FindSubmatch(out); m != nil {
		ms, _ := strconv.ParseFloat(string(m[1]), 64)
		return time.Duration(ms * float64(time.Millisecond)), true
	}
	// -q hides per-packet lines; fall back to the summary average.
	if m := regexp.MustCompile(`= [\d.]+/([\d.]+)/`).FindSubmatch(out); m != nil {
		ms, _ := strconv.ParseFloat(string(m[1]), 64)
		return time.Duration(ms * float64(time.Millisecond)), true
	}
	return 0, false
}

// monitorHealth pings the far inner address of every tunnel and records the
// result, so the panel knows whether traffic really passes.
func monitorHealth(ctx context.Context, tr *health.Tracker) {
	t := time.NewTicker(healthEvery)
	defer t.Stop()
	for {
		st, err := config.Load()
		if err == nil {
			ts, _ := st.Effective()
			keep := map[string]bool{}
			var wg sync.WaitGroup
			for _, tn := range ts {
				far, ok := farInner(st, tn)
				if !ok {
					continue
				}
				keep[tn.Name] = true
				if !engine.State(engine.Ifname(tn.Name)).Up {
					tr.Record(tn.Name, false, 0)
					continue
				}
				wg.Add(1)
				go func(name, addr string) {
					defer wg.Done()
					rtt, up := pingOnce(ctx, addr)
					tr.Record(name, up, rtt)
				}(tn.Name, far)
			}
			wg.Wait()
			tr.Forget(keep)
			tr.Save()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// farInner is the inner address of the tunnel's far end, the thing to ping.
func farInner(st *config.State, t config.Tunnel) (string, bool) {
	me := 0
	if st.IsSelf(t.Sides[1].Node) {
		me = 1
	}
	n := t.Net4
	if n == "" {
		n = t.Net6
	}
	a, ok := config.Inner(n, 1-me)
	if !ok {
		return "", false
	}
	return a.Addr().String(), true
}
