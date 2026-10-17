package daemon

import (
	"context"
	"log"
	"net/netip"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/eoip"
	"neotunnel/internal/forward"
	"neotunnel/internal/obfs"
	"neotunnel/internal/pmtu"
	"neotunnel/internal/relay"
	"neotunnel/internal/tcptun"
	"neotunnel/internal/traffic"
)

// dataPlane is the daemon's engine.Userspace: NeoObfs, TCP and EoIP tunnels
// and PROXY protocol relays.
type dataPlane struct {
	obfs  *obfs.Manager
	tcp   *tcptun.Manager
	eoip  *eoip.Manager
	relay *relay.Manager

	benchMu sync.Mutex
	bench   map[string]closer // temporary tunnels of speed tests
}

type closer interface{ Close() }

func newDataPlane() *dataPlane {
	return &dataPlane{obfs: obfs.NewManager(), tcp: tcptun.NewManager(), eoip: eoip.NewManager(), relay: relay.NewManager(), bench: map[string]closer{}}
}

// keepalive turns a spec's setting (0 default, -1 off) into a duration.
func keepalive(s engine.LinkSpec, def time.Duration) time.Duration {
	switch {
	case s.Keepalive == 0:
		return def
	case s.Keepalive < 0:
		return 0
	}
	return time.Duration(s.Keepalive) * time.Second
}

func obfsConfig(s engine.LinkSpec) obfs.Config {
	return obfs.Config{
		Name: s.Name, Tunnel: s.Tunnel, Secret: s.Secret, Side: s.Side,
		Remote: netip.AddrPortFrom(s.Remote, s.Port), Port: s.Port,
		Padding: s.Padding, Keepalive: keepalive(s, 25*time.Second),
	}
}

func tcpConfig(s engine.LinkSpec) tcptun.Config {
	return tcptun.Config{
		Name: s.Name, Tunnel: s.Tunnel, Secret: s.Secret, Side: s.Side,
		Remote: netip.AddrPortFrom(s.Remote, s.Port), Port: s.Port,
		Cipher: s.Cipher, Keepalive: keepalive(s, 15*time.Second), Streams: tcpStreams(s.Profile),
	}
}

func eoipConfig(s engine.LinkSpec) eoip.Config {
	return eoip.Config{
		Name: s.Name, Tunnel: s.Tunnel, Side: s.Side, ID: uint16(s.Key),
		Local: s.Local, Remote: s.Remote, Port: s.Port,
		TTL: s.TTL, DSCP: s.DSCP, NoDF: s.NoDF, Keepalive: keepalive(s, 25*time.Second),
	}
}

func (d *dataPlane) SyncLinks(specs []engine.LinkSpec) []error {
	var oc []obfs.Config
	var tc []tcptun.Config
	var ec []eoip.Config
	for _, s := range specs {
		switch s.Kind {
		case config.KindTCP:
			tc = append(tc, tcpConfig(s))
		case config.KindEoIP:
			ec = append(ec, eoipConfig(s))
		default:
			oc = append(oc, obfsConfig(s))
		}
	}
	errs := append(d.obfs.Sync(oc), d.tcp.Sync(tc)...)
	return append(errs, d.eoip.Sync(ec)...)
}

func (d *dataPlane) StartBench(s engine.LinkSpec, expiry time.Time) error {
	d.StopBench([]string{s.Name})
	var l closer
	var err error
	switch s.Kind {
	case config.KindTCP:
		l, err = tcptun.Start(tcpConfig(s))
	case config.KindEoIP:
		l, err = eoip.Start(eoipConfig(s))
	default:
		l, err = obfs.Start(obfsConfig(s))
	}
	if err != nil {
		return err
	}
	d.benchMu.Lock()
	d.bench[s.Name] = l
	d.benchMu.Unlock()
	time.AfterFunc(time.Until(expiry), func() {
		d.benchMu.Lock()
		cur := d.bench[s.Name]
		if cur == l {
			delete(d.bench, s.Name)
		}
		d.benchMu.Unlock()
		if cur == l {
			l.Close()
		}
	})
	return nil
}

func (d *dataPlane) StopBench(names []string) {
	d.benchMu.Lock()
	var stop []closer
	for _, n := range names {
		if l, ok := d.bench[n]; ok {
			stop = append(stop, l)
			delete(d.bench, n)
		}
	}
	d.benchMu.Unlock()
	for _, l := range stop {
		l.Close()
	}
}

func (d *dataPlane) SyncRelays(specs []relay.Spec) []error { return d.relay.Sync(specs) }

func (d *dataPlane) Close() {
	d.relay.Close()
	d.eoip.Close()
	d.obfs.Close()
	d.tcp.Close()
	d.StopBench(d.benchNames())
}

const (
	sampleEvery = 10 * time.Second
	saveEvery   = time.Minute
	probeEvery  = 5 * time.Minute
	probeMaxAge = 30 * time.Minute
)

// account samples tunnel counters into the traffic store until ctx ends.
func account(ctx context.Context, store *traffic.Store, changed func()) {
	t := time.NewTicker(sampleEvery)
	defer t.Stop()
	lastSave := time.Now()
	for {
		now := time.Now()
		if name, ok := traffic.TakeResetRequest(); ok {
			store.Reset(name, now)
		}
		// Speed tests leave the totals. Their requests are written after the
		// test, so taking them before this sample means their bytes are in.
		excludes := traffic.TakeExcludes()
		if st, err := config.Load(); err == nil {
			ts, fs := st.Effective()
			names := make([]string, 0, len(ts))
			for _, tn := range ts {
				names = append(names, tn.Name)
				if ls := engine.State(engine.Ifname(tn.Name)); ls.Exists {
					store.Sample(tn.Name, counters(ls), now)
				}
			}
			if cs := forward.ReadCounters(); cs != nil {
				for _, f := range fs {
					n := forward.CounterName(f.Proto, f.Ports)
					names = append(names, traffic.Port+f.Proto+"/"+f.Ports)
					u, d := cs[n+"u"], cs[n+"d"]
					store.Sample(traffic.Port+f.Proto+"/"+f.Ports,
						traffic.Pair{Rx: d.Bytes, Tx: u.Bytes, RxP: d.Packets, TxP: u.Packets}, now)
				}
			}
			store.Keep(names, now)
			enforceQuotas(ts, store, now, changed)
		}
		if nic := engine.PublicIface(); nic != "" {
			if ls := engine.State(nic); ls.Exists {
				store.Sample(traffic.NIC, counters(ls), now)
			}
		}
		for _, e := range excludes {
			store.Subtract(e.Tunnel, e.Pair, now)
		}
		if time.Since(lastSave) >= saveEvery {
			if err := store.Save(); err != nil {
				log.Printf("save traffic: %v", err)
			}
			lastSave = time.Now()
		}
		select {
		case <-ctx.Done():
			store.Save()
			return
		case <-t.C:
		}
	}
}

// probeMTUs keeps path MTU measurements fresh for tunnels that ask for it,
// and triggers a reconcile when one changed.
func probeMTUs(ctx context.Context, changed func()) {
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		if st, err := config.Load(); err == nil {
			ts, _ := st.Effective()
			for _, tn := range ts {
				if !tn.MTUProbe || tn.Over != "" {
					continue
				}
				me := 0
				if st.IsSelf(tn.Sides[1].Node) {
					me = 1
				}
				remote, err := netip.ParseAddr(tn.Sides[1-me].Endpoint)
				if err != nil || pmtu.Age(remote) < probeMaxAge {
					continue
				}
				pctx, cancel := context.WithTimeout(ctx, time.Minute)
				m, err := pmtu.Probe(pctx, remote, nil)
				cancel()
				if err != nil {
					continue
				}
				if pmtu.Save(remote, m) {
					log.Printf("path MTU to %s is %d", remote, m)
					changed()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func counters(ls engine.LinkState) traffic.Pair {
	return traffic.Pair{Rx: ls.RxBytes, Tx: ls.TxBytes, RxP: ls.RxPkts, TxP: ls.TxPkts}
}

// enforceQuotas takes down the tunnels that used their monthly quota and
// brings them back when the month (or the quota) changes.
func enforceQuotas(ts []config.Tunnel, store *traffic.Store, now time.Time, changed func()) {
	var sums map[string]traffic.Summary
	over := map[string]bool{}
	for _, t := range ts {
		if t.QuotaGB <= 0 {
			continue
		}
		if sums == nil {
			sums = store.Summaries(now)
		}
		m := sums[t.Name].Month
		if float64(m.Rx+m.Tx) >= t.QuotaGB*(1<<30) { // the panel's GB
			over[t.Name] = true
		}
	}
	diff := false
	for _, t := range ts {
		if over[t.Name] != engine.Blocked(t.Name) {
			diff = true
			if over[t.Name] {
				log.Printf("tunnel %s: monthly quota of %g GB used; down until next month", t.Name, t.QuotaGB)
			} else if t.QuotaGB > 0 || engine.Blocked(t.Name) {
				log.Printf("tunnel %s: back up, quota available", t.Name)
			}
		}
	}
	if diff {
		engine.SetBlocked(over)
		changed()
	}
}

func (d *dataPlane) benchNames() []string {
	d.benchMu.Lock()
	defer d.benchMu.Unlock()
	var out []string
	for n := range d.bench {
		out = append(out, n)
	}
	return out
}

// tcpStreams is how many connections a TCP tunnel uses for its profile:
// more connections carry more at once, fewer keep each flow's queue short.
func tcpStreams(profile string) int {
	switch profile {
	case config.ProfileLean:
		return 2
	case config.ProfileMax:
		return 8
	}
	return 4
}
