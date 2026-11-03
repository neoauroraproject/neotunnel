package mtr

import (
	"context"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"
)

// A real trace, where a test sets a path up: NT_MTR_TARGET and the hops
// to it in NT_MTR_HOPS (see test/mtr.sh).
func TestRunReal(t *testing.T) {
	target := os.Getenv("NT_MTR_TARGET")
	if target == "" {
		t.Skip("NT_MTR_TARGET not set")
	}
	want, _ := strconv.Atoi(os.Getenv("NT_MTR_HOPS"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Run(ctx, netip.MustParseAddr(target), 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", r)
	if !r.Reached || len(r.Hops) != want {
		t.Fatalf("reached %v, %d hops, want %d", r.Reached, len(r.Hops), want)
	}
	for _, h := range r.Hops {
		if h.Host == "" || h.Got == 0 || h.Sent != 3 {
			t.Errorf("hop %+v", h)
		}
	}
	if r.Hops[len(r.Hops)-1].Host != target {
		t.Errorf("last hop %s, want %s", r.Hops[len(r.Hops)-1].Host, target)
	}
}
