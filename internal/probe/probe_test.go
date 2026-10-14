package probe

import (
	"context"
	"net"
	"testing"
	"time"
)

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	// Find a port free for both TCP and UDP.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	s := NewServer()
	go s.Serve(addr)
	time.Sleep(100 * time.Millisecond)
	return s, addr
}

func TestLatencyAndBandwidth(t *testing.T) {
	s, addr := startServer(t)
	tok := NewToken()
	s.Open(tok, time.Minute)
	ctx := context.Background()

	r, err := Latency(ctx, addr, tok, 20, 5*time.Millisecond, 64)
	if err != nil {
		t.Fatal(err)
	}
	if r.Received != 20 || r.Loss() != 0 || r.Avg <= 0 || r.Min > r.Max {
		t.Errorf("unexpected latency result %+v", r)
	}

	for _, dir := range []Direction{Upload, Download} {
		bps, err := Bandwidth(ctx, addr, tok, dir, 300*time.Millisecond, 2)
		if err != nil {
			t.Fatalf("%c: %v", dir, err)
		}
		if bps < 1e6 {
			t.Errorf("%c: implausibly slow loopback: %.0f bit/s", dir, bps)
		}
	}
}

func TestRejectsUnknownToken(t *testing.T) {
	s, addr := startServer(t)
	s.Open(NewToken(), time.Minute)
	if _, err := Latency(context.Background(), addr, NewToken(), 3, 5*time.Millisecond, 64); err == nil {
		t.Error("probe answered without a valid token")
	}
	if _, err := Bandwidth(context.Background(), addr, NewToken(), Download, 200*time.Millisecond, 1); err == nil {
		t.Error("bandwidth served without a valid token")
	}
}
