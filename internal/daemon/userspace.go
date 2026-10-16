package daemon

import (
	"context"
	"log"
	"net/netip"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/forward"
	"neotunnel/internal/obfs"
	"neotunnel/internal/pmtu"
	"neotunnel/internal/relay"
	"neotunnel/internal/traffic"
)

// dataPlane is the daemon's engine.Userspace: NeoObfs tunnels and PROXY
// protocol relays.
type dataPlane struct {
	obfs  *obfs.Manager
	relay *relay.Manager
}

func newDataPlane() *dataPlane { return &dataPlane{obfs: obfs.NewManager(), relay: relay.NewManager()} }

func (d *dataPlane) SyncLinks(specs []engine.LinkSpec) []error {
	cfgs := make([]obfs.Config, 0, len(specs))
	for _, s := range specs {
		ka := time.Duration(s.Keepalive) * time.Second
		if s.Keepalive == 0 {
			ka = 25 * time.Second
		} else if s.Keepalive < 0 {
			ka = 0
		}
		cfgs = append(cfgs, obfs.Config{
			Name: s.Name, Tunnel: s.Tunnel, Secret: s.Secret, Side: s.Side,
			Remote: netip.AddrPortFrom(s.Remote, s.Port), Port: s.Port,
			Padding: s.Padding, Keepalive: ka,
		})
	}
	return d.obfs.Sync(cfgs)
}

func (d *dataPlane) SyncRelays(specs []relay.Spec) []error { return d.relay.Sync(specs) }

func (d *dataPlane) Close() {
	d.relay.Close()
	d.obfs.Close()
}

const (
	sampleEvery = 10 * time.Second
	saveEvery   = time.Minute
	probeEvery  = 5 * time.Minute
	probeMaxAge = 30 * time.Minute
)

// account samples tunnel counters into the traffic store until ctx ends.
func account(ctx context.Context, store *traffic.Store) {
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
