// Package bench measures tunnels between the master and a peer: it brings up
// temporary tunnels of each kind on both ends, measures latency, jitter,
// loss and throughput through them, tears them down and ranks the results.
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/probe"
	"neotunnel/internal/traffic"
)

type Options struct {
	Pings    int
	Interval time.Duration
	Duration time.Duration // per direction
	Streams  int
	// Live, if set, receives measurements as they happen: phase "ping"
	// (milliseconds), "upload" or "download" (bits/s), "done".
	Live func(phase string, value float64)
}

func DefaultOptions() Options {
	return Options{Pings: 60, Interval: 15 * time.Millisecond, Duration: 4 * time.Second, Streams: 4}
}

type Result struct {
	Name     string
	Title    string
	Mode     string // quick mode that builds this tunnel
	Port     uint16 // UDP port it ran on, if any
	MTU      int
	Latency  probe.LatencyResult
	Up, Down float64 // bits/s
	Err      string
	// Status is where a comparison is with this candidate (see Compare).
	Status string
}

// MarshalJSON flattens durations to milliseconds for the panel.
func (r Result) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name     string  `json:"name"`
		Title    string  `json:"title"`
		Mode     string  `json:"mode,omitempty"`
		Port     uint16  `json:"port,omitempty"`
		MTU      int     `json:"mtu,omitempty"`
		PingMs   float64 `json:"ping_ms"`
		JitterMs float64 `json:"jitter_ms"`
		MinMs    float64 `json:"min_ms"`
		MaxMs    float64 `json:"max_ms"`
		Loss     float64 `json:"loss"`
		Up       float64 `json:"up_bps"`
		Down     float64 `json:"down_bps"`
		Err      string  `json:"error,omitempty"`
		Status   string  `json:"status,omitempty"`
	}{r.Name, r.Title, r.Mode, r.Port, r.MTU, ms(r.Latency.Avg), ms(r.Latency.Jitter), ms(r.Latency.Min), ms(r.Latency.Max),
		r.Latency.Loss(), r.Up, r.Down, r.Err, r.Status})
}

// Score ranks by the slower direction, discounting loss.
func (r Result) Score() float64 {
	if r.Err != "" {
		return -1
	}
	return min(r.Up, r.Down) * (1 - r.Latency.Loss()/100)
}

// Candidate is one tunnel configuration under test; Tunnels are listed
// underlay first and the last one carries the test traffic.
type Candidate struct {
	Name    string
	Title   string
	Mode    string
	Port    uint16
	Tunnels []config.Tunnel
}

// BenchPort is the port used to test tunnels wrapped in UDP (and, +3, the
// TCP transport).
const BenchPort = 40417

// Candidates returns the tests for an IPv4 underlay between self and peer.
func Candidates(self, peer string, selfAddr, peerAddr string) []Candidate {
	sides := [2]config.Side{{Node: self, Endpoint: selfAddr}, {Node: peer, Endpoint: peerAddr}}
	i := 0
	mk := func(name string, kind config.Kind, over string, port uint16) config.Tunnel {
		i++
		t := config.Tunnel{Name: name, Kind: kind, Over: over, Port: port, Key: 0x4e540000 + uint32(i), VNI: 0x4e5400 + uint32(i)}
		if over == "" {
			t.Sides = sides
		} else {
			t.Sides = [2]config.Side{{Node: self}, {Node: peer}}
		}
		ki, _ := config.LookupKind(kind)
		if ki.Carry4 {
			t.Net4 = fmt.Sprintf("10.78.%d.0/30", i)
		}
		if ki.Carry6 {
			t.Net6 = fmt.Sprintf("fd4e:5400:%x::/64", i)
		}
		return t
	}
	sit, ipip6, ip6gre := config.KindSIT, config.KindIPIP6, config.KindIP6GRE
	return []Candidate{
		{"6to4", "6to4 (SIT + IPIP6)", "6to4", 0, []config.Tunnel{mk("6to4-c", sit, "", 0), mk("6to4", ipip6, "6to4-c", 0)}},
		{"6to4-udp", fmt.Sprintf("6to4 on UDP %d", BenchPort), "6to4", BenchPort,
			[]config.Tunnel{mk("6to4u-c", sit, "", BenchPort), mk("6to4u", ipip6, "6to4u-c", 0)}},
		{"6to4-gre", "6to4 (SIT + GRE6)", "6to4-gre", 0, []config.Tunnel{mk("6to4g-c", sit, "", 0), mk("6to4g", ip6gre, "6to4g-c", 0)}},
		{"ipip", "IPIP", "ipip", 0, []config.Tunnel{mk("ipip", config.KindIPIP, "", 0)}},
		{"gre", "GRE", "gre", 0, []config.Tunnel{mk("gre", config.KindGRE, "", 0)}},
		{"gretap", "GRETAP (L2)", "gretap", 0, []config.Tunnel{mk("gretap", config.KindGRETAP, "", 0)}},
		{"vxlan", "VXLAN (L2)", "vxlan", 0, []config.Tunnel{mk("vxlan", config.KindVXLAN, "", 0)}},
		{"geneve", "GENEVE (L2)", "geneve", 0, []config.Tunnel{mk("geneve", config.KindGENEVE, "", 0)}},
		{"wireguard", "WireGuard", "wireguard", BenchPort + 1, []config.Tunnel{secret(mk("wg", config.KindWireGuard, "", BenchPort+1))}},
		{"l2tpv3", "L2TPv3 (L2)", "l2tpv3", 0, []config.Tunnel{mk("l2tp", config.KindL2TPv3, "", 0)}},
		{"l2tpv3-udp", fmt.Sprintf("L2TPv3 on UDP %d", BenchPort+2), "l2tpv3", BenchPort + 2, []config.Tunnel{mk("l2tpu", config.KindL2TPv3, "", BenchPort+2)}},
		{"tcp", fmt.Sprintf("TCP %d", BenchPort+3), "tcp", BenchPort + 3, []config.Tunnel{secret(mk("tcp", config.KindTCP, "", BenchPort+3))}},
	}
}

// Runner holds an open probe session on the peer.
type Runner struct {
	ID     *identity.Identity
	State  *config.State
	Peer   config.Peer
	Opts   Options
	client *agent.Client
	token  probe.Token
	// Progress receives human-readable step updates.
	Progress func(string)
	// CheckOnly stops after the connectivity check: a few seconds per
	// candidate, for the tunnel wizard.
	CheckOnly bool
}

func (r *Runner) say(f string, a ...any) {
	if r.Progress != nil {
		r.Progress(fmt.Sprintf(f, a...))
	}
}

func (r *Runner) live(phase string, v float64) {
	if r.Opts.Live != nil {
		r.Opts.Live(phase, v)
	}
}

func (r *Runner) open(ctx context.Context) error {
	if r.client != nil {
		return nil
	}
	c, err := agent.ToPeer(r.ID, r.Peer)
	if err != nil {
		return err
	}
	r.token = probe.NewToken()
	if err := c.OpenProbe(ctx, r.token.String(), 15*time.Minute); err != nil {
		return fmt.Errorf("open test session on %s: %w", r.Peer.Name, err)
	}
	r.client = c
	return nil
}

// Measure runs latency then both throughput directions against addr.
func (r *Runner) Measure(ctx context.Context, addr netip.Addr) (probe.LatencyResult, float64, float64, error) {
	if err := r.open(ctx); err != nil {
		return probe.LatencyResult{}, 0, 0, err
	}
	hp := net.JoinHostPort(addr.String(), strconv.Itoa(probe.DefaultPort))
	r.live("ping", 0)
	lat, err := probe.Latency(ctx, hp, r.token, r.Opts.Pings, r.Opts.Interval, 64)
	if err != nil {
		return lat, 0, 0, err
	}
	r.say("  latency %.1f ms, jitter %.1f ms, loss %.0f%%", ms(lat.Avg), ms(lat.Jitter), lat.Loss())
	r.live("ping", ms(lat.Avg))
	up, err := probe.BandwidthLive(ctx, hp, r.token, probe.Upload, r.Opts.Duration, r.Opts.Streams, func(bps float64) { r.live("upload", bps) })
	if err != nil {
		return lat, 0, 0, fmt.Errorf("upload: %w", err)
	}
	r.say("  upload %s", Rate(up))
	down, err := probe.BandwidthLive(ctx, hp, r.token, probe.Download, r.Opts.Duration, r.Opts.Streams, func(bps float64) { r.live("download", bps) })
	if err != nil {
		return lat, up, 0, fmt.Errorf("download: %w", err)
	}
	r.say("  download %s", Rate(down))
	return lat, up, down, nil
}

// Baseline measures the plain internet path, without any tunnel.
func (r *Runner) Baseline(ctx context.Context) Result {
	res := Result{Name: "direct", Title: "Direct (no tunnel)"}
	a, err := netip.ParseAddr(r.Peer.Address)
	if err != nil {
		res.Err = "peer address is not an IP"
		return res
	}
	r.say("direct path to %s", a)
	res.Latency, res.Up, res.Down, err = r.Measure(ctx, a)
	if err != nil {
		res.Err = err.Error()
	}
	return res
}

// existing finds a persistent tunnel that blocks a temporary one: the kernel
// allows a single keyless (SIT/IPIP) tunnel per endpoint pair. It returns the
// tunnel to measure instead, or an error if nothing equivalent runs.
func (r *Runner) existing(c Candidate) (*config.Tunnel, error) {
	base, top := c.Tunnels[0], c.Tunnels[len(c.Tunnels)-1]
	if base.Kind != config.KindSIT && base.Kind != config.KindIPIP {
		return nil, nil
	}
	for _, t := range r.State.Tunnels {
		if t.Kind != base.Kind || t.Over != "" {
			continue
		}
		if _, ok := t.SideOf(r.Peer.Name); !ok {
			continue
		}
		if t.Port != base.Port {
			return nil, fmt.Errorf("skipped: tunnel %s (%s) already uses these two servers' addresses", t.Name, t.Kind)
		}
		if len(c.Tunnels) == 1 {
			return &t, nil
		}
		for _, u := range r.State.Tunnels {
			if u.Over == t.Name && u.Kind == top.Kind {
				return &u, nil
			}
		}
		return nil, fmt.Errorf("skipped: tunnel %s (%s) to %s already uses this carrier", t.Name, t.Kind, r.Peer.Name)
	}
	return nil, nil
}

// Run tests one candidate end to end and always cleans up.
func (r *Runner) Run(ctx context.Context, c Candidate) Result {
	res := Result{Name: c.Name, Title: c.Title, Mode: c.Mode, Port: c.Port}
	r.say("%s", c.Title)
	if err := r.open(ctx); err != nil {
		res.Err = err.Error()
		return res
	}
	if t, err := r.existing(c); err != nil {
		res.Err = err.Error()
		return res
	} else if t != nil {
		r.say("  measuring existing tunnel %s", t.Name)
		res.Title += " [" + t.Name + "]"
		res.MTU = engine.State(engine.Ifname(t.Name)).MTU
		far := 1
		if r.State.IsSelf(t.Sides[1].Node) {
			far = 0
		}
		n := t.Net4
		if n == "" {
			n = t.Net6
		}
		target, _ := config.Inner(n, far)
		var err error
		if res.Latency, res.Up, res.Down, err = r.MeasureTunnel(ctx, t.Name, target.Addr()); err != nil {
			res.Err = err.Error()
		}
		return res
	}
	if needsDaemon(c) {
		res.Err = "skipped: " + engine.ErrNeedsDaemon.Error()
		return res
	}

	up, err := r.client.BenchUp(ctx, agent.BenchUpRequest{Self: r.Peer.Name, Tunnels: c.Tunnels, TTL: 600})
	var peerNames []string
	for _, s := range up.Specs {
		peerNames = append(peerNames, s.Name)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.client.BenchDown(cctx, peerNames)
	}()
	if err != nil {
		res.Err = "peer: " + err.Error()
		return res
	}

	isSelf := func(n string) bool { return n == r.State.Self }
	specs, errs := engine.RenderTunnels(isSelf, c.Tunnels, engine.RouteSource)
	if len(errs) > 0 {
		res.Err = errs[0].Error()
		return res
	}
	var names []string
	for i := range specs {
		specs[i].Name = agent.BenchIfname(specs[i].Tunnel)
		names = append(names, specs[i].Name)
	}
	defer engine.RemoveBench(names)
	if err := engine.ApplyBench(specs, 10*time.Minute); err != nil {
		res.Err = err.Error()
		return res
	}
	top := specs[len(specs)-1]
	res.MTU = top.MTU

	last := c.Tunnels[len(c.Tunnels)-1]
	n := last.Net4
	if n == "" {
		n = last.Net6
	}
	target, _ := config.Inner(n, 1)
	if err := r.waitUp(ctx, target.Addr()); err != nil {
		res.Err = err.Error()
		return res
	}
	res.Latency, res.Up, res.Down, err = r.Measure(ctx, target.Addr())
	if err != nil {
		res.Err = err.Error()
	}
	return res
}

func (r *Runner) waitUp(ctx context.Context, a netip.Addr) error {
	hp := net.JoinHostPort(a.String(), strconv.Itoa(probe.DefaultPort))
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if l, err := probe.Latency(ctx, hp, r.token, 3, 100*time.Millisecond, 64); err == nil && l.Received > 0 {
			return nil
		}
	}
	return fmt.Errorf("no traffic passes (protocol filtered on the path?)")
}

// Rank sorts best first.
func Rank(rs []Result) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Score() > rs[j].Score() })
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func Rate(bps float64) string {
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.2f Gbit/s", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f Mbit/s", bps/1e6)
	default:
		return fmt.Sprintf("%.0f kbit/s", bps/1e3)
	}
}

// Table renders results as aligned text.
func Table(rs []Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %6s %9s %9s %6s %13s %13s\n", "TUNNEL", "MTU", "PING", "JITTER", "LOSS", "UPLOAD", "DOWNLOAD")
	for _, r := range rs {
		if r.Err != "" {
			what := "failed"
			if r.Status == StatusSkipped {
				what = "skipped"
			}
			fmt.Fprintf(&b, "%-22s  %s: %s\n", r.Title, what, strings.TrimPrefix(r.Err, "skipped: "))
			continue
		}
		mtu := "-"
		if r.MTU > 0 {
			mtu = strconv.Itoa(r.MTU)
		}
		fmt.Fprintf(&b, "%-22s %6s %7.1fms %7.1fms %5.0f%% %13s %13s\n", r.Title, mtu,
			ms(r.Latency.Avg), ms(r.Latency.Jitter), r.Latency.Loss(), Rate(r.Up), Rate(r.Down))
	}
	return b.String()
}

// MeasureTunnel measures through an existing tunnel and keeps the test's
// bytes out of the tunnel's traffic totals, here and on the peer (which
// sees the same bytes the other way round).
func (r *Runner) MeasureTunnel(ctx context.Context, tunnel string, addr netip.Addr) (probe.LatencyResult, float64, float64, error) {
	before := engine.TunnelState(tunnel)
	lat, up, down, err := r.Measure(ctx, addr)
	after := engine.TunnelState(tunnel)
	if before.Exists && after.Exists && after.RxBytes >= before.RxBytes && after.TxBytes >= before.TxBytes {
		d := traffic.Pair{Rx: after.RxBytes - before.RxBytes, Tx: after.TxBytes - before.TxBytes,
			RxP: after.RxPkts - before.RxPkts, TxP: after.TxPkts - before.TxPkts}
		traffic.RequestExclude(tunnel, d)
		if r.client != nil {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.client.ExcludeTraffic(cctx, tunnel, traffic.Pair{Rx: d.Tx, Tx: d.Rx, RxP: d.TxP, TxP: d.RxP})
			cancel()
		}
	}
	return lat, up, down, err
}

// secret gives a temporary tunnel the key material its kind needs.
func secret(t config.Tunnel) config.Tunnel {
	t.Secret = config.NewSecret()
	return t
}

// needsDaemon reports whether a candidate has user-space layers (TCP,
// NeoObfs) that only the daemon's data plane can bring up.
func needsDaemon(c Candidate) bool {
	if engine.InDaemon() {
		return false
	}
	for _, t := range c.Tunnels {
		if ki, err := config.LookupKind(t.Kind); err == nil && ki.Userspace {
			return true
		}
	}
	return false
}
