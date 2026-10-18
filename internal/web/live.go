package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/firewall"
	"neotunnel/internal/fleet"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/pmtu"
	"neotunnel/internal/sysctl"
	"neotunnel/internal/traffic"
	"neotunnel/internal/version"
)

// Live is the Backend for a real node.
type Live struct {
	ID      *identity.Identity
	Started time.Time
	// Problems returns the daemon's latest reconcile errors.
	Problems func() []string
	// Traffic is the daemon's traffic accounting (nil: totals unavailable).
	Store *traffic.Store
	// Health is the daemon's per-tunnel ping/loss tracker.
	Health *health.Tracker
	// Reconcile asks the daemon to apply the state now.
	Reconcile func()
}

func (l *Live) Overview() (Overview, error) {
	st, err := manage.Node()
	if err != nil {
		return Overview{}, err
	}
	ov := Overview{
		Node: NodeInfo{
			Name: st.Self, Address: st.Address, Address6: engine.PublicIPv6(), Version: version.Version, Arch: version.Arch(),
			Key: identity.Fingerprint(l.ID.PublicString()),
		},
		Revision: st.Revision,
		Modes:    manage.Modes(),
		Ciphers:  config.Ciphers(),
		Defaults: st.D(),
		Install:  "curl -fsSL " + version.InstallURL + " | sudo bash",
		Repo:     version.RepoURL,
		Time:     time.Now().UnixMilli(),
	}
	readSystem(&ov.Node)
	if cc, _ := sysctl.Get("net.ipv4.tcp_congestion_control"); cc == "bbr" {
		ov.Node.BBR = true
	}
	if m := st.Managed; m != nil {
		ov.Master = &MasterInfo{Name: m.Master.Name, Address: m.Master.Address, Paired: m.EnrollToken == ""}
	}
	if p := st.Panel; p != nil {
		ov.Panel = PanelInfo{URL: PanelURL(st), Port: p.Port, User: p.User}
	}
	if l.Problems != nil {
		ov.Errors = l.Problems()
	}
	var sums map[string]traffic.Summary
	if l.Store != nil {
		sums = l.Store.Summaries(time.Now())
		if s, ok := sums[traffic.NIC]; ok {
			ov.Node.NIC, ov.Node.NICToday, ov.Node.NICMonth = engine.PublicIface(), s.Today, s.Month
		}
	}
	var hs map[string]health.State
	if l.Health != nil {
		hs = l.Health.Snapshot()
	}
	ov.Tunnels = tunnelViews(st, sums, hs)
	ov.Peers = peerViews(st)
	ov.Forwards = forwardViews(st, sums)
	ov.Commands = commands(st)
	return ov, nil
}

func tunnelViews(st *config.State, sums map[string]traffic.Summary, hs map[string]health.State) []TunnelView {
	ts, _ := st.Effective()
	_, renderErrs := engine.Render(st, engine.RouteSource)
	carriers := map[string]bool{}
	for _, t := range ts {
		if t.Over != "" {
			carriers[t.Over] = true
		}
	}
	out := []TunnelView{}
	for _, t := range ts {
		me := 0
		if st.IsSelf(t.Sides[1].Node) {
			me = 1
		}
		far := t.Sides[1-me]
		v := TunnelView{
			Name: t.Name, Kind: string(t.Kind), Mode: t.Mode, Over: t.Over, Carrier: carriers[t.Name],
			Peer: far.Node, FarIP: far.Endpoint, Port: t.Port,
			Limits: manage.LimitsOf(t), Blocked: engine.Blocked(t.Name),
		}
		if ki, err := config.LookupKind(t.Kind); err == nil {
			v.Layer = ki.Layer
		}
		v.Settings, v.BasePort = manage.SettingsOf(st, t.Name)
		stack := manage.Stack(st, t.Name)
		for _, n := range stack {
			bt, ok := tunnelByName(ts, n)
			if !ok {
				continue
			}
			ls := engine.State(engine.Ifname(n))
			v.Stack = append(v.Stack, LayerView{Name: n, Kind: string(bt.Kind), MTU: ls.MTU, Up: ls.Up})
			if bt.Kind == config.KindObfs {
				v.Obfs = true
			}
			if bt.Over == "" {
				// The public address of the server at the bottom of the stack.
				v.FarIP = bt.Sides[1-me].Endpoint
				switch {
				case bt.Encrypt != "":
					v.Encrypt = bt.Encrypt
				case bt.Kind == config.KindWireGuard:
					v.Encrypt = "wireguard"
				case bt.Kind == config.KindObfs:
					v.Encrypt = "xchacha20-poly1305"
				}
				if bt.MTUProbe {
					if a, err := netip.ParseAddr(v.FarIP); err == nil {
						v.PathMTU = pmtu.Cached(a)
					}
				}
			}
		}
		if t.Kind == config.KindWireGuard {
			v.WGKey, _ = engine.WireGuardPublicKey(t, me)
		}
		if t.Kind == config.KindVXLAN || t.Kind == config.KindGENEVE {
			v.VNI = t.VNI
		}
		if _, local := st.Tunnel(t.Name); !local {
			v.Managed = true
			if st.Managed != nil {
				v.Peer = st.Managed.Master.Name
			}
		}
		if p, ok := config.Inner(t.Net4, me); ok {
			v.Here4 = p.String()
			q, _ := config.Inner(t.Net4, 1-me)
			v.There4 = q.Addr().String()
		}
		if p, ok := config.Inner(t.Net6, me); ok {
			v.Here6 = p.String()
			q, _ := config.Inner(t.Net6, 1-me)
			v.There6 = q.Addr().String()
		}
		ls := engine.TunnelState(t.Name)
		v.Up, v.MTU, v.Rx, v.Tx = ls.Up, ls.MTU, ls.RxBytes, ls.TxBytes
		if h, ok := hs[t.Name]; ok && h.Probes > 0 {
			v.Health = &HealthView{Ping: h.AvgMs, Loss: h.Loss, Passes: h.Passes, Last: h.Last}
		}
		if s, ok := sums[t.Name]; ok {
			v.Total, v.Today, v.Month, v.Tests = s.Total, s.Today, s.Month, s.Tests
		}
		if under, err := netip.ParseAddr(v.FarIP); err == nil {
			v.Overhead = stackOverhead(ts, stack, under.Is6(), true)
			v.Wire = wire(v.Today, v.Overhead)
		}
		for _, e := range renderErrs {
			if containsTunnel(e.Error(), t.Name) {
				v.Error = e.Error()
			}
		}
		out = append(out, v)
	}
	return out
}

func tunnelByName(ts []config.Tunnel, name string) (config.Tunnel, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return config.Tunnel{}, false
}

func containsTunnel(msg, name string) bool {
	return strings.HasPrefix(msg, "tunnel "+name+":")
}

func peerViews(st *config.State) []PeerView {
	out := []PeerView{}
	for _, p := range st.Peers {
		v := PeerView{
			Name: p.Name, Address: p.Address, Address6: p.Address6, Paired: p.Paired(),
			InSync: p.Paired() && p.AppliedRevision == st.Revision, Applied: p.AppliedRevision,
			Version: p.Version, Arch: p.Arch,
		}
		if !p.LastSeen.IsZero() {
			v.LastSeen = p.LastSeen.Unix()
		}
		for _, t := range st.Tunnels {
			if _, ok := t.SideOf(p.Name); ok && t.Mode != "" {
				v.Tunnels++
			}
		}
		out = append(out, v)
	}
	return out
}

func forwardViews(st *config.State, sums map[string]traffic.Summary) []ForwardView {
	ts, fs := st.Effective()
	out := []ForwardView{}
	for _, f := range fs {
		v := ForwardView{Tunnel: f.Tunnel, Proto: f.Proto, Ports: f.Ports, To: f.To, Public: st.Address,
			Mode: f.ModeOf(), ProxyV: f.ProxyVersion}
		if v.Mode == config.ForwardProxy && v.ProxyV == 0 {
			v.ProxyV = 2
		}
		if s, ok := sums[traffic.Port+f.Proto+"/"+f.Ports]; ok {
			v.Rx, v.Tx, v.Today = s.Total.Rx, s.Total.Tx, s.Today
		}
		if t, ok := tunnelByName(ts, f.Tunnel); ok {
			me := 0
			if st.IsSelf(t.Sides[1].Node) {
				me = 1
			}
			v.Peer = t.Sides[1-me].Node
			n := t.Net4
			if n == "" {
				n = t.Net6
			}
			if a, ok := config.Inner(n, 1-me); ok {
				v.Target = a.Addr().String()
			}
			if v.Mode == config.ForwardRelay {
				v.Target = "127.0.0.1" // the far node connects to its own loopback
			}
		}
		out = append(out, v)
	}
	return out
}

// commands lists the CLI equivalents of what the panel does, filled with
// this node's real names and addresses.
func commands(st *config.State) []CommandGroup {
	here := st.Self
	g := []CommandGroup{{
		Title: "روی همین سرور (" + here + ")",
		Commands: []Command{
			{"وضعیت کلی تانل‌ها و سرورها", "sudo neotunnel status", here},
			{"منوی تعاملی", "sudo neotunnel", here},
			{"ترافیک زنده و مجموع مصرف هر تانل", "sudo neotunnel traffic", here},
			{"آدرس و نام کاربری پنل", "sudo neotunnel panel info", here},
			{"تغییر رمز پنل", "sudo neotunnel panel password", here},
			{"بهینه‌سازی شبکه (BBR)", "sudo neotunnel boost", here},
			{"ارسال دوباره‌ی تنظیمات به همه‌ی سرورها", "sudo neotunnel peer sync", here},
			{"ارتقای سرورها به نسخه‌ی همین سرور", "sudo neotunnel peer upgrade", here},
			{"به‌روزرسانی NeoTunnel به آخرین نسخه", "sudo neotunnel update", here},
			{"حذف کامل NeoTunnel از این سرور", "sudo neotunnel uninstall", here},
		},
	}}
	for _, p := range st.Peers {
		cg := CommandGroup{Title: "سرور " + p.Name + " (" + p.Address + ")"}
		cg.Commands = append(cg.Commands,
			Command{"تانل 6to4 تا این سرور با فوروارد پورت 443", fmt.Sprintf("sudo neotunnel quick %s --address %s --forward 443", p.Name, p.Address), here},
			Command{"تانل 6to4 روی پورت UDP دلخواه (مثلاً 3000)", fmt.Sprintf("sudo neotunnel quick %s --port 3000", p.Name), here},
			Command{"تانل رمزنگاری‌شده (IPsec) با MTU خودکار", fmt.Sprintf("sudo neotunnel quick %s --encrypt aes-gcm-128 --mtu-probe", p.Name), here},
			Command{"تانل مبهم‌شده روی UDP (NeoObfs)", fmt.Sprintf("sudo neotunnel quick %s --obfs --port 8443", p.Name), here},
			Command{"فوروارد 443 با رد کردن IP واقعی کاربر", fmt.Sprintf("sudo neotunnel quick %s --forward 443 --forward-mode transparent", p.Name), here},
			Command{"مقایسه‌ی سرعت همه‌ی انواع تانل تا این سرور", "sudo neotunnel bench " + p.Name, here},
			Command{"دستور نصب/همگام‌سازی برای اجرا روی " + p.Name, "sudo neotunnel peer connect " + p.Name, here},
		)
		for _, t := range st.Tunnels {
			if _, ok := t.SideOf(p.Name); ok && t.Mode != "" {
				cg.Commands = append(cg.Commands,
					Command{"تست سرعت تانل " + t.Name, "sudo neotunnel test " + t.Name, here},
					Command{"پیدا کردن بهترین MTU تانل " + t.Name, "sudo neotunnel mtu " + t.Name + " --apply", here})
			}
		}
		g = append(g, cg)
	}
	return g
}

func (l *Live) AddPeer(name, address string) (fleet.Setup, error) {
	if err := manage.AddPeer(name, address); err != nil {
		return fleet.Setup{}, err
	}
	return manage.Setup(name)
}

func (l *Live) RemovePeer(name string) (manage.Change, error) { return manage.RemovePeer(name) }

func (l *Live) PeerSetup(name string) (fleet.Setup, error) { return manage.Setup(name) }

func (l *Live) SyncPeers(names []string) []manage.PeerSync {
	st, err := manage.Node()
	if err != nil {
		return []manage.PeerSync{{Error: err.Error()}}
	}
	if len(names) == 0 {
		for _, p := range st.Peers {
			names = append(names, p.Name)
		}
	}
	return manage.Sync(st, names)
}

func (l *Live) UpgradePeer(ctx context.Context, name string) (string, error) {
	st, err := manage.Node()
	if err != nil {
		return "", err
	}
	return fleet.Upgrade(ctx, l.ID, st, name)
}

func (l *Live) CreateTunnel(req manage.TunnelRequest) (manage.Change, error) {
	_, ch, err := manage.CreateTunnel(req)
	return ch, err
}

func (l *Live) UpdateTunnel(name string, set manage.Settings, port uint16) (manage.Change, error) {
	return manage.UpdateTunnel(name, set, port)
}

func (l *Live) SetLimits(name string, lim manage.Limits) (manage.Change, error) {
	ch, err := manage.SetLimits(name, lim)
	if err == nil && l.Reconcile != nil {
		l.Reconcile()
	}
	return ch, err
}

func (l *Live) RemoveTunnel(name string) (manage.Change, error) {
	_, ch, err := manage.RemoveTunnel(name)
	return ch, err
}

func (l *Live) AddForward(req manage.ForwardRequest) (manage.Change, error) {
	return manage.AddForward(req)
}

func (l *Live) UpdateForward(req manage.ForwardRequest) (manage.Change, error) {
	return manage.UpdateForward(req)
}

func (l *Live) RemoveForward(tunnel, ports string) (manage.Change, error) {
	return manage.RemoveForward(tunnel, ports)
}

func (l *Live) SetNode(name, address string) error { return manage.SetNode(name, address) }

func (l *Live) SetDefaults(d config.Defaults) error { return manage.SetDefaults(d) }

func (l *Live) Login(user, pass string) bool {
	st, err := config.Load()
	if err != nil || st.Panel == nil {
		return false
	}
	// Always run bcrypt so timing doesn't reveal valid user names.
	ok := checkPassword(st.Panel.PassHash, pass)
	return ok && subtle.ConstantTimeCompare([]byte(user), []byte(st.Panel.User)) == 1
}

func (l *Live) ChangePassword(current, next string) error {
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = config.Update(func(s *config.State) error {
		if s.Panel == nil || !checkPassword(s.Panel.PassHash, current) {
			return fmt.Errorf("current password is wrong")
		}
		s.Panel.PassHash = hash
		return nil
	})
	return err
}

func (l *Live) SetUser(user string) error {
	user = strings.TrimSpace(user)
	if len(user) < 3 || len(user) > 32 || strings.ContainsAny(user, " \t:/") {
		return fmt.Errorf("username must be 3-32 characters without spaces")
	}
	_, err := config.Update(func(s *config.State) error {
		if s.Panel == nil {
			return fmt.Errorf("panel is not set up")
		}
		s.Panel.User = user
		return nil
	})
	return err
}

func (l *Live) SetPanelPort(port int) (string, error) {
	if !PortFree(port) {
		return "", fmt.Errorf("port %d is in use or reserved", port)
	}
	st, err := config.Update(func(s *config.State) error {
		if s.Panel == nil {
			return fmt.Errorf("panel is not set up")
		}
		s.Panel.Port = port
		return nil
	})
	if err != nil {
		return "", err
	}
	return PanelURL(st), nil
}

func (l *Live) TestTunnel(ctx context.Context, name string, progress func(string), live func(string, float64)) ([]bench.Result, error) {
	st, err := manage.Node()
	if err != nil {
		return nil, err
	}
	o := bench.DefaultOptions()
	o.Live = live
	r, err := bench.TestTunnel(ctx, l.ID, st, name, o, progress)
	if err != nil {
		return nil, err
	}
	return []bench.Result{r}, nil
}

func (l *Live) Bench(ctx context.Context, peer string, only []string, progress func(string), live func(string, float64), partial func(bench.Result)) ([]bench.Result, error) {
	st, err := manage.Node()
	if err != nil {
		return nil, err
	}
	p, ok := st.Peer(peer)
	if !ok {
		return nil, fmt.Errorf("unknown server %q", peer)
	}
	if !p.Paired() {
		return nil, fmt.Errorf("server %s is not connected yet: run its setup command first", peer)
	}
	if _, err := netip.ParseAddr(st.Address); err != nil {
		return nil, fmt.Errorf("this server's public address is unknown; set it in settings")
	}
	want := map[string]bool{}
	for _, o := range only {
		want[o] = true
	}
	o := bench.DefaultOptions()
	o.Live = live
	r := &bench.Runner{ID: l.ID, State: st, Peer: *p, Opts: o, Progress: progress}
	var cands []bench.Candidate
	for _, c := range bench.Candidates(st.Self, p.Name, st.Address, p.Address) {
		if len(want) == 0 || want[c.Name] {
			cands = append(cands, c)
		}
	}
	// Show every candidate as pending right away; the direct path goes first
	// as the reference.
	if partial != nil {
		for _, c := range cands {
			partial(bench.Result{Name: c.Name, Title: c.Title, Mode: c.Mode, Port: c.Port, Status: bench.StatusPending})
		}
	}
	var results []bench.Result
	if len(want) == 0 || want["direct"] {
		if partial != nil {
			partial(bench.Result{Name: "direct", Title: "Direct (no tunnel)", Status: bench.StatusMeasuring})
		}
		d := r.Baseline(ctx)
		d.Status = bench.StatusDone
		if d.Err != "" {
			d.Status = bench.StatusFailed
		}
		if partial != nil {
			partial(d)
		}
		results = append(results, d)
	}
	results = append(results, r.Compare(ctx, cands, partial)...)
	bench.Rank(results)
	return results, ctx.Err()
}

// ProbeMTU measures the path under a tunnel's stack, remembers the result
// (tunnels with MTU probing follow it) and works out the best MTU.
func (l *Live) ProbeMTU(ctx context.Context, name string) (MTUReport, error) {
	st, err := manage.Node()
	if err != nil {
		return MTUReport{}, err
	}
	return MeasureMTU(ctx, st, name, l.Reconcile)
}

// MeasureMTU is shared with the CLI.
func MeasureMTU(ctx context.Context, st *config.State, name string, changed func()) (MTUReport, error) {
	stack := manage.Stack(st, name)
	ts, _ := st.Effective()
	bottom, ok := tunnelByName(ts, stack[0])
	if !ok {
		return MTUReport{}, fmt.Errorf("unknown tunnel %q", name)
	}
	me := 0
	if st.IsSelf(bottom.Sides[1].Node) {
		me = 1
	}
	remote, err := netip.ParseAddr(bottom.Sides[1-me].Endpoint)
	if err != nil {
		return MTUReport{}, fmt.Errorf("tunnel %s has no public far end to measure", name)
	}
	m, err := pmtu.Probe(ctx, remote, nil)
	if err != nil {
		return MTUReport{}, err
	}
	if pmtu.Save(remote, m) && bottom.MTUProbe && changed != nil {
		changed()
	}
	rep := MTUReport{Remote: remote.String(), PathMTU: m, Current: engine.State(engine.Ifname(name)).MTU, Probing: bottom.MTUProbe,
		Overhead: stackOverhead(ts, stack, remote.Is6(), false)}
	rep.Best = m - rep.Overhead
	return rep, nil
}

func (l *Live) Traffic() map[string]traffic.Summary {
	if l.Store == nil {
		return map[string]traffic.Summary{}
	}
	return l.Store.Summaries(time.Now())
}

func (l *Live) ResetTraffic(tunnel string) error {
	if l.Store == nil {
		return fmt.Errorf("traffic accounting runs in the service")
	}
	l.Store.Reset(tunnel, time.Now())
	return l.Store.Save()
}

func (l *Live) System() (SystemInfo, error) {
	var si SystemInfo
	keys := make([]string, 0, len(sysctl.Boost))
	for k := range sysctl.Boost {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	si.Boosted = true
	for _, k := range keys {
		have, _ := sysctl.Get(k)
		have = strings.Join(strings.Fields(have), " ")
		si.Sysctl = append(si.Sysctl, SysctlView{Key: k, Want: sysctl.Boost[k], Have: have})
		if have != sysctl.Boost[k] {
			si.Boosted = false
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		si.Kernel = strings.TrimSpace(string(b))
	}
	_, err := exec.LookPath("nft")
	si.Nft = err == nil
	si.Ufw = firewall.Active()
	return si, nil
}

func (l *Live) Boost() error { return sysctl.ApplyBoost() }

// Backup returns the state file, keys and secrets included.
func (l *Live) Backup() ([]byte, error) {
	st, err := config.Load()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(st, "", "  ")
}

// stackOverhead is what every layer of a stack (bottom first) adds to a
// packet. Worst case for MTU sizing; with average padding for traffic
// estimates.
func stackOverhead(ts []config.Tunnel, stack []string, under6, avgPad bool) int {
	o := 0
	for _, n := range stack {
		t, _ := tunnelByName(ts, n)
		ki, err := config.LookupKind(t.Kind)
		if err != nil {
			continue
		}
		o += ki.OverheadFor(under6 && t.Over == "", t.Port != 0 && t.Over == "")
		if t.Kind == config.KindObfs {
			if avgPad {
				o += t.Padding / 2
			} else {
				o += t.Padding
			}
		}
		if t.Encrypt != "" && t.Kind == config.KindTCP {
			o += config.TCPTagOverhead
		} else if t.Encrypt != "" {
			o += config.ESPOverhead
		}
	}
	return o
}

// wire estimates the bytes a tunnel's traffic takes on the network,
// encapsulation included: payload plus the per-packet overhead.
func wire(p traffic.Pair, overhead int) uint64 {
	return p.Rx + p.Tx + (p.RxP+p.TxP)*uint64(overhead)
}

// Check tells within seconds whether a tunnel would connect, before it is
// created: the same stack is brought up on both ends under temporary names
// and probed. With all set it checks every kind at once instead.
func (l *Live) Check(ctx context.Context, req manage.TunnelRequest, all, speed bool) ([]bench.Result, error) {
	st, err := manage.Node()
	if err != nil {
		return nil, err
	}
	if req.Peer == "" {
		return nil, fmt.Errorf("a quick check needs NeoTunnel on the far end")
	}
	p, ok := st.Peer(req.Peer)
	if !ok {
		return nil, fmt.Errorf("unknown server %q", req.Peer)
	}
	if !p.Paired() {
		return nil, fmt.Errorf("server %s is not connected yet", req.Peer)
	}
	opts := bench.DefaultOptions()
	opts.Duration = 3 * time.Second
	r := &bench.Runner{ID: l.ID, State: st, Peer: *p, Opts: opts, CheckOnly: !speed || all}
	if all {
		return r.Compare(ctx, bench.Candidates(st.Self, p.Name, st.Address, p.Address), nil), nil
	}
	if req.Obfs || req.Mode == "obfs" {
		// NeoObfs runs in the daemon, not as a temporary kernel tunnel.
		return []bench.Result{{Name: "check", Mode: req.Mode, Status: "skipped",
			Err: "NeoObfs is checked once created: use the tunnel's speed test"}}, nil
	}
	req.Name, req.Forward = "nwz", ""
	_, defs, err := manage.PlanTunnel(st, req)
	if err != nil {
		return nil, err
	}
	for i := range defs {
		t := &defs[i]
		ki, _ := config.LookupKind(t.Kind)
		if ki.Carry4 {
			t.Net4 = fmt.Sprintf("10.79.%d.0/30", i+1)
		}
		if ki.Carry6 {
			t.Net6 = fmt.Sprintf("fd4e:5700:%x::/64", i+1)
		}
		t.Key, t.VNI = 0x4e570000+uint32(i), 0x4e5700+uint32(i)
		if t.NeedsSecret() && t.Secret == "" {
			t.Secret = config.NewSecret()
		}
	}
	c := bench.Candidate{Name: "check", Title: req.Mode, Mode: req.Mode, Port: defs[0].Port, Tunnels: defs}
	return r.Compare(ctx, []bench.Candidate{c}, nil), nil
}

// RestartTunnel takes a tunnel and the layers under it down and up again,
// on this node and on the far server, without changing any setting.
func (l *Live) RestartTunnel(ctx context.Context, name string) error {
	st, err := manage.Node()
	if err != nil {
		return err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	names := manage.Stack(st, name)
	var peerErr error
	if p, ok := st.Peer(t.Sides[1].Node); ok && p.Paired() {
		if c, err := agent.ToPeer(l.ID, *p); err != nil {
			peerErr = err
		} else if err := c.Restart(ctx, names); err != nil && !strings.Contains(err.Error(), "404") {
			peerErr = fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	engine.Restart(names, func() {
		if cur, err := config.Load(); err == nil {
			engine.Apply(cur)
		}
	})
	return peerErr
}
