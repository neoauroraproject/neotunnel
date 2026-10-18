package tcptun

import (
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
)

// TestBenchEnd runs one end of a real TCP tunnel until SIGTERM, for
// test/tcptun-bench.sh (which runs the compiled test binary in two network
// namespaces). Skipped unless NT_BENCH_SIDE is set.
func TestBenchEnd(t *testing.T) {
	sideS := os.Getenv("NT_BENCH_SIDE")
	if sideS == "" {
		t.Skip("NT_BENCH_SIDE not set")
	}
	side, _ := strconv.Atoi(sideS)
	streams, _ := strconv.Atoi(os.Getenv("NT_BENCH_STREAMS"))
	port, _ := strconv.Atoi(os.Getenv("NT_BENCH_PORT"))
	c := Config{
		Name:    os.Getenv("NT_BENCH_DEV"),
		Tunnel:  "bench",
		Secret:  "YmVuY2gtc2VjcmV0LWJlbmNoLXNlY3JldA==",
		Side:    side,
		Remote:  netip.MustParseAddrPort(os.Getenv("NT_BENCH_REMOTE")),
		Port:    uint16(port),
		Cipher:  os.Getenv("NT_BENCH_CIPHER"),
		Streams: streams,
	}
	l, err := Start(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	t.Logf("dropped %d packets", l.DroppedPackets.Load())
	l.Close()
}
