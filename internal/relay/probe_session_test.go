package relay

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/probe"
	"neotunnel/internal/transport"
)

// benchPair runs a speed test's temporary connection tunnel between two
// managers on loopback, the listening side (1, or 0 when reverse) on a free
// port, and has both hubs send probe streams to probeAddr.
func benchPair(t *testing.T, kind config.Kind, reverse bool, probeAddr string) [2]*transport.Hub {
	t.Helper()
	port := freeTCP(t)
	secret := config.NewSecret()
	hubs := [2]*transport.Hub{transport.NewHub(), transport.NewHub()}
	listener := 1
	if reverse {
		listener = 0
	}
	for i := range 2 {
		m := newManager(int(freeTCP(t)))
		m.SetHub(hubs[i])
		hubs[i].SetProbe(probeAddr)
		t.Cleanup(m.Close)
		t.Cleanup(hubs[i].Close)
		s := transport.Spec{Name: "ntx-t1", Tunnel: "ntx-t1", Secret: secret, Side: i, Kind: kind, Remote: lo, Conns: 2, Dial: i != listener}
		s.Ports[0] = port
		if err := hubs[i].StartBench(s); err != nil {
			t.Fatal(err)
		}
		if err := m.SyncListen(); err != nil {
			t.Fatal(err)
		}
	}
	return hubs
}

// A speed test over a connection tunnel: pings over its sessions, and both
// directions of the bandwidth test on streams the far end carries to its
// probe server; the real tunnels Sync keeps are left alone.
func TestSessionProbe(t *testing.T) {
	pport := freeTCP(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(pport)))
	ps := probe.NewServer()
	go ps.Serve(addr)
	tok := probe.NewToken()
	ps.Open(tok, time.Minute)

	for _, kind := range []config.Kind{config.KindStreamTCP, config.KindStreamTLS} {
		for _, reverse := range []bool{false, true} {
			t.Run(string(kind)+" reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
				hubs := benchPair(t, kind, reverse, addr)
				ctx := context.Background()
				ping := func() (time.Duration, error) { return hubs[0].Ping("ntx-t1", 2*time.Second) }
				lat, err := probe.PingLatency(ctx, ping, 10, 10*time.Millisecond)
				if err != nil || lat.Received != 10 {
					t.Fatalf("latency %+v: %v", lat, err)
				}
				dial := func(context.Context) (net.Conn, error) { return hubs[0].OpenProbe("ntx-t1") }
				for _, dir := range []probe.Direction{probe.Upload, probe.Download} {
					bps, err := probe.BandwidthOver(ctx, dial, tok, dir, 2*time.Second, 8, nil)
					if err != nil || bps <= 0 {
						t.Fatalf("%c: %v bit/s, %v", dir, bps, err)
					}
				}
				// Sync, which runs the real tunnels, leaves the test's be.
				if errs := hubs[0].Sync(nil); len(errs) > 0 {
					t.Fatal(errs)
				}
				if _, err := hubs[0].Ping("ntx-t1", 2*time.Second); err != nil {
					t.Fatalf("after Sync: %v", err)
				}
				hubs[0].StopBench([]string{"ntx-t1"})
				if _, err := hubs[0].OpenProbe("ntx-t1"); err == nil {
					t.Fatal("a stopped test tunnel still opens streams")
				}
			})
		}
	}
}
