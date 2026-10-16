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

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/firewall"
	"neotunnel/internal/fleet"
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
			Name: st.Self, Address: st.Address, Version: version.Version, Arch: version.Arch(),
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
	}
	ov.Tunnels = tunnelViews(st, sums)
	ov.Peers = peerViews(st)
	ov.Forwards = forwardViews(st)
	ov.Commands = commands(st)
	return ov, nil
}

func tunnelViews(st *config.State, sums map[string]traffic.Summary) []TunnelView {
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
		ls := engine.State(engine.Ifname(t.Name))
		v.Up, v.MTU, v.Rx, v.Tx = ls.Up, ls.MTU, ls.RxBytes, ls.TxBytes
		if s, ok := sums[t.Name]; ok {
			v.Total, v.Today, v.Month = s.Total, s.Today, s.Month
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
			Name: p.Name, Address: p.Address, Paired: p.Paired(),
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

func forwardViews(st *config.State) []ForwardView {
	ts, fs := st.Effective()
	out := []ForwardView{}
	for _, f := range fs {
		v := ForwardView{Tunnel: f.Tunnel, Proto: f.Proto, Ports: f.Ports, To: f.To, Public: st.Address,
			Mode: f.ModeOf(), ProxyV: f.ProxyVersion}
		if v.Mode == config.ForwardProxy && v.ProxyV == 0 {
			v.ProxyV = 2
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

func (l *Live) Bench(ctx context.Context, peer string, only []string, progress func(string), live func(string, float64)) ([]bench.Result, error) {
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
	var results []bench.Result
	if len(want) == 0 || want["direct"] {
		results = append(results, r.Baseline(ctx))
	}
	for _, c := range bench.Candidates(st.Self, p.Name, st.Address, p.Address) {
		if len(want) > 0 && !want[c.Name] {
			continue
		}
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		results = append(results, r.Run(ctx, c))
	}
	bench.Rank(results)
	return results, nil
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
	rep := MTUReport{Remote: remote.String(), PathMTU: m, Current: engine.State(engine.Ifname(name)).MTU, Probing: bottom.MTUProbe}
	for _, n := range stack {
		t, _ := tunnelByName(ts, n)
		ki, err := config.LookupKind(t.Kind)
		if err != nil {
			continue
		}
		rep.Overhead += ki.OverheadFor(remote.Is6() && t.Over == "", t.Port != 0 && t.Over == "")
		if t.Kind == config.KindObfs {
			rep.Overhead += t.Padding
		}
		if t.Encrypt != "" {
			rep.Overhead += config.ESPOverhead
		}
	}
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
