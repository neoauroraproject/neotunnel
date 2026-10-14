// Package demo is a Backend with sample data, for screenshots and UI work.
// Nothing here touches the system.
package demo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"neotunnel/internal/bench"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
	"neotunnel/internal/probe"
	"neotunnel/internal/web"
)

type Backend struct {
	mu      sync.Mutex
	start   time.Time
	ov      web.Overview
	install string
}

func New() *Backend {
	b := &Backend{start: time.Now()}
	b.install = "curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash"
	b.ov = web.Overview{
		Node: web.NodeInfo{
			Name: "tehran", Address: "192.0.2.10", Version: "v0.1.0", Arch: "amd64", Key: "Xk3a9Qâ€¦P0f2Lw",
			Uptime: 13*86400 + 5*3600, Load: [3]float64{0.42, 0.37, 0.31}, CPUs: 4,
			MemTotal: 8 << 30, MemUsed: 2_900 << 20, BBR: true,
		},
		Revision: 14,
		Peers: []web.PeerView{
			{Name: "germany", Address: "198.51.100.24", Paired: true, InSync: true, Applied: 14, Version: "v0.1.0", Arch: "amd64", Tunnels: 1},
			{Name: "netherlands", Address: "203.0.113.57", Paired: true, InSync: true, Applied: 14, Version: "v0.1.0", Arch: "amd64", Tunnels: 1},
			{Name: "finland", Address: "198.51.100.80", Paired: false, Tunnels: 1},
		},
		Tunnels: []web.TunnelView{
			{Name: "de-6", Kind: "sit", Layer: 3, Carrier: true, Peer: "germany", FarIP: "198.51.100.24", Here6: "fd3c:91a2:7e10:1::1/64", There6: "fd3c:91a2:7e10:1::2", MTU: 1480, Up: true},
			{Name: "de", Kind: "ipip6", Mode: "6to4", Layer: 3, Over: "de-6", Peer: "germany", FarIP: "198.51.100.24", Here4: "10.77.0.1/30", There4: "10.77.0.2", MTU: 1440, Up: true},
			{Name: "nl-6", Kind: "sit", Layer: 3, Carrier: true, Peer: "netherlands", FarIP: "203.0.113.57", Port: 3000, Here6: "fd3c:91a2:7e10:2::1/64", There6: "fd3c:91a2:7e10:2::2", MTU: 1472, Up: true},
			{Name: "nl", Kind: "ipip6", Mode: "6to4", Layer: 3, Over: "nl-6", Peer: "netherlands", FarIP: "203.0.113.57", Here4: "10.77.0.5/30", There4: "10.77.0.6", MTU: 1432, Up: true},
			{Name: "fi", Kind: "vxlan", Mode: "vxlan", Layer: 2, Peer: "finland", FarIP: "198.51.100.80", Port: 4789, Here4: "10.77.0.9/30", There4: "10.77.0.10", Here6: "fd3c:91a2:7e10:3::1/64", There6: "fd3c:91a2:7e10:3::2", MTU: 1450},
		},
		Forwards: []web.ForwardView{
			{Tunnel: "de", Peer: "germany", Proto: "both", Ports: "443", Target: "10.77.0.2", Public: "192.0.2.10"},
			{Tunnel: "de", Peer: "germany", Proto: "tcp", Ports: "8443", Target: "10.77.0.2", Public: "192.0.2.10"},
			{Tunnel: "nl", Peer: "netherlands", Proto: "tcp", Ports: "2053", To: 443, Target: "10.77.0.6", Public: "192.0.2.10"},
			{Tunnel: "nl", Peer: "netherlands", Proto: "both", Ports: "2083-2087", Target: "10.77.0.6", Public: "192.0.2.10"},
		},
		Modes:   manage.Modes(),
		Panel:   web.PanelInfo{URL: "https://192.0.2.10:34127/k7m2xq9p4d/", Port: 34127, User: "admin"},
		Install: b.install,
		Repo:    "https://github.com/neoauroraproject/neotunnel",
	}
	b.ov.Commands = []web.CommandGroup{
		{Title: "Ø±ÙˆÛŒ Ù‡Ù…ÛŒÙ† Ø³Ø±ÙˆØ± (tehran)", Commands: []web.Command{
			{Label: "ÙˆØ¶Ø¹ÛŒØª Ú©Ù„ÛŒ ØªØ§Ù†Ù„â€ŒÙ‡Ø§ Ùˆ Ø³Ø±ÙˆØ±Ù‡Ø§", Run: "sudo neotunnel status", Where: "tehran"},
			{Label: "ØªØ±Ø§ÙÛŒÚ© Ø²Ù†Ø¯Ù‡â€ŒÛŒ Ù‡Ø± ØªØ§Ù†Ù„", Run: "sudo neotunnel traffic", Where: "tehran"},
			{Label: "Ø¢Ø¯Ø±Ø³ Ùˆ Ù†Ø§Ù… Ú©Ø§Ø±Ø¨Ø±ÛŒ Ù¾Ù†Ù„", Run: "sudo neotunnel panel info", Where: "tehran"},
		}},
		{Title: "Ø³Ø±ÙˆØ± germany (198.51.100.24)", Commands: []web.Command{
			{Label: "ØªØ§Ù†Ù„ 6to4 ØªØ§ Ø§ÛŒÙ† Ø³Ø±ÙˆØ± Ø¨Ø§ ÙÙˆØ±ÙˆØ§Ø±Ø¯ Ù¾ÙˆØ±Øª 443", Run: "sudo neotunnel quick germany --address 198.51.100.24 --forward 443", Where: "tehran"},
			{Label: "Ù…Ù‚Ø§ÛŒØ³Ù‡â€ŒÛŒ Ø³Ø±Ø¹Øª Ù‡Ù…Ù‡â€ŒÛŒ Ø§Ù†ÙˆØ§Ø¹ ØªØ§Ù†Ù„ ØªØ§ Ø§ÛŒÙ† Ø³Ø±ÙˆØ±", Run: "sudo neotunnel bench germany", Where: "tehran"},
			{Label: "ØªØ³Øª Ø³Ø±Ø¹Øª ØªØ§Ù†Ù„ de", Run: "sudo neotunnel test de", Where: "tehran"},
		}},
	}
	return b
}

func (b *Backend) Overview() (web.Overview, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Counters grow like real traffic so the panel shows live rates.
	el := time.Since(b.start).Seconds()
	rates := map[string][2]float64{"de": {11.8e6, 2.1e6}, "nl": {6.4e6, 1.3e6}}
	ov := b.ov
	ov.Tunnels = append([]web.TunnelView(nil), b.ov.Tunnels...)
	for i, t := range ov.Tunnels {
		if r, ok := rates[t.Name]; ok {
			ov.Tunnels[i].Rx = uint64(214e9 + r[0]*el*(1+0.1*sinish(el)))
			ov.Tunnels[i].Tx = uint64(38e9 + r[1]*el)
		}
	}
	ov.Peers[0].LastSeen = time.Now().Add(-40 * time.Second).Unix()
	ov.Peers[1].LastSeen = time.Now().Add(-2 * time.Minute).Unix()
	ov.Time = time.Now().UnixMilli()
	return ov, nil
}

func sinish(x float64) float64 {
	x = x - float64(int(x/6))*6
	return (x - 3) / 3
}

func (b *Backend) setup(name string) fleet.Setup {
	for _, p := range b.ov.Peers {
		if p.Name == name {
			link := "neotunnel://H4sIAAAAAAAC_6xUTW-jNhD9K5HPgAcYbâ€¦" + name
			s := fleet.Setup{Peer: name, Address: p.Address, Paired: p.Paired, Link: link, Apply: "sudo neotunnel apply '" + link + "'"}
			if !p.Paired {
				s.Install = "curl -fsSk https://192.0.2.10:7710/dl/9f2câ€¦ -o /tmp/neotunnel && echo '5be1â€¦  /tmp/neotunnel' | sha256sum -c --quiet && sudo install -m 755 /tmp/neotunnel /usr/local/bin/neotunnel && rm /tmp/neotunnel && " + s.Apply
				s.GitHub = b.install + " -s -- --no-panel && " + s.Apply
			}
			return s
		}
	}
	return fleet.Setup{}
}

func (b *Backend) AddPeer(name, address string) (fleet.Setup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ov.Peers = append(b.ov.Peers, web.PeerView{Name: name, Address: address})
	return b.setup(name), nil
}

func (b *Backend) RemovePeer(string) (manage.Change, error) { return manage.Change{}, nil }

func (b *Backend) PeerSetup(name string) (fleet.Setup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.setup(name), nil
}

func (b *Backend) SyncPeers([]string) []manage.PeerSync {
	return []manage.PeerSync{{Peer: "germany", OK: true, Revision: 14}, {Peer: "netherlands", OK: true, Revision: 14}}
}

func (b *Backend) UpgradePeer(context.Context, string) (string, error) { return "v0.1.0", nil }

func (b *Backend) CreateTunnel(req manage.TunnelRequest) (manage.Change, error) {
	return manage.Change{Peers: []manage.PeerSync{{Peer: req.Peer, OK: true, Revision: 15}}}, nil
}

func (b *Backend) RemoveTunnel(string) (manage.Change, error) { return manage.Change{}, nil }

func (b *Backend) AddForward(string, string, string, uint16) (manage.Change, error) {
	return manage.Change{}, nil
}

func (b *Backend) RemoveForward(string, string) (manage.Change, error) { return manage.Change{}, nil }

func (b *Backend) SetNode(string, string) error { return nil }

func lat(avg, jitter float64, loss int) probe.LatencyResult {
	d := func(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }
	return probe.LatencyResult{Sent: 100, Received: 100 - loss, Min: d(avg - 2*jitter), Avg: d(avg), Max: d(avg + 6*jitter), Jitter: d(jitter)}
}

func (b *Backend) TestTunnel(ctx context.Context, name string, progress func(string)) ([]bench.Result, error) {
	progress("latency 46.2 ms, jitter 0.9 ms, loss 0%")
	time.Sleep(400 * time.Millisecond)
	progress("upload 182.4 Mbit/s")
	progress("download 611.0 Mbit/s")
	return []bench.Result{{Name: name, Title: name, Mode: "6to4", MTU: 1440, Latency: lat(46.2, 0.9, 0), Up: 182.4e6, Down: 611e6}}, nil
}

func (b *Backend) Bench(ctx context.Context, peer string, only []string, progress func(string)) ([]bench.Result, error) {
	steps := []bench.Result{
		{Name: "6to4", Title: "6to4 (SIT + IPIP6)", Mode: "6to4", MTU: 1440, Latency: lat(46.1, 0.8, 0), Up: 188e6, Down: 642e6},
		{Name: "ipip", Title: "IPIP", Mode: "ipip", MTU: 1480, Latency: lat(46.0, 0.8, 0), Up: 184e6, Down: 631e6},
		{Name: "6to4-udp", Title: "6to4 on UDP 40417", Mode: "6to4", Port: bench.BenchPort, MTU: 1432, Latency: lat(46.4, 1.0, 0), Up: 179e6, Down: 604e6},
		{Name: "gre", Title: "GRE", Mode: "gre", MTU: 1472, Latency: lat(46.3, 0.9, 0), Up: 176e6, Down: 598e6},
		{Name: "direct", Title: "Direct (no tunnel)", Latency: lat(45.8, 0.7, 0), Up: 192e6, Down: 655e6},
		{Name: "vxlan", Title: "VXLAN (L2)", Mode: "vxlan", MTU: 1450, Latency: lat(46.9, 1.2, 0), Up: 161e6, Down: 547e6},
		{Name: "6to4-gre", Title: "6to4 (SIT + GRE6)", Mode: "6to4-gre", MTU: 1432, Latency: lat(46.5, 1.0, 0), Up: 170e6, Down: 571e6},
		{Name: "gretap", Title: "GRETAP (L2)", Mode: "gretap", MTU: 1458, Latency: lat(47.0, 1.1, 1), Up: 150e6, Down: 529e6},
		{Name: "geneve", Title: "GENEVE (L2)", Mode: "geneve", MTU: 1450, Latency: lat(47.2, 1.3, 0), Up: 0, Down: 0, Err: "no traffic passes (protocol filtered on the path?)"},
	}
	for _, r := range steps {
		progress(r.Title)
		if r.Err == "" {
			progress(fmt.Sprintf("  latency %.1f ms, jitter %.1f ms, loss %.0f%%", float64(r.Latency.Avg)/1e6, float64(r.Latency.Jitter)/1e6, r.Latency.Loss()))
			progress("  upload " + bench.Rate(r.Up))
			progress("  download " + bench.Rate(r.Down))
		} else {
			progress("  " + r.Err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	bench.Rank(steps)
	return steps, nil
}

func (b *Backend) Login(user, pass string) bool        { return user == "admin" && pass == "demo" }
func (b *Backend) ChangePassword(string, string) error { return nil }
func (b *Backend) SetPanelPort(port int) (string, error) {
	return fmt.Sprintf("https://192.0.2.10:%d/k7m2xq9p4d/", port), nil
}
