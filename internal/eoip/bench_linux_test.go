package eoip

import (
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
)

// TestBenchEnd runs one end of a real EoIP tunnel until SIGTERM, for
// test/tcptun-bench.sh (KIND=eoip, port 0 = raw GRE). Skipped unless
// NT_BENCH_SIDE is set.
func TestBenchEnd(t *testing.T) {
	sideS := os.Getenv("NT_BENCH_SIDE")
	if sideS == "" {
		t.Skip("NT_BENCH_SIDE not set")
	}
	side, _ := strconv.Atoi(sideS)
	port, _ := strconv.Atoi(os.Getenv("NT_BENCH_EOIP_PORT"))
	remote := netip.MustParseAddrPort(os.Getenv("NT_BENCH_REMOTE"))
	l, err := Start(Config{
		Name:   os.Getenv("NT_BENCH_DEV"),
		Tunnel: "bench",
		Side:   side,
		ID:     77,
		Remote: remote.Addr(),
		Port:   uint16(port),
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	t.Logf("bad packets %d", l.BadPackets.Load())
	l.Close()
}
