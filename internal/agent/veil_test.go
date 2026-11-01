package agent

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
	"neotunnel/internal/probe"
)

// filter stands in for a path that stalls TLS: it forwards to target, keeps
// what clients send first, and when dropTLS is set never forwards a stream
// that starts like a TLS handshake.
type filter struct {
	l       net.Listener
	target  string
	dropTLS bool
	stalled atomic.Int32
	conns   atomic.Int32
	mu      sync.Mutex
	first   [][]byte
}

func startFilter(t *testing.T, target string, dropTLS bool) *filter {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &filter{l: l, target: target, dropTLS: dropTLS}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.conns.Add(1)
			go f.handle(c)
		}
	}()
	return f
}

func (f *filter) handle(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.first = append(f.first, append([]byte(nil), buf[:n]...))
	f.mu.Unlock()
	if f.dropTLS && buf[0] == tlsHandshakeRecord {
		f.stalled.Add(1)
		io.Copy(io.Discard, c) // TCP stays up; nothing comes back
		return
	}
	up, err := net.Dial("tcp", f.target)
	if err != nil {
		return
	}
	defer up.Close()
	up.Write(buf[:n])
	go io.Copy(up, c)
	io.Copy(c, up)
}

func peerOf(id *identity.Identity, addr string) []string { return []string{"https://" + addr} }

func edgeState(t *testing.T, masterKey string) {
	t.Helper()
	config.Save(&config.State{Self: "edge", Managed: &config.Managed{
		Master: config.MasterRef{Name: "hub", PublicKey: masterKey}, Self: "edge", Revision: 7,
	}})
}

// The case from real servers: TCP connects and TLS stalls. Calls still get
// through, one after another, because nothing on the wire looks like TLS.
func TestVeilGetsPastAPathThatStallsTLS(t *testing.T) {
	masterID := newIdentity(t)
	peerID := newIdentity(t)
	edgeState(t, masterID.PublicString())
	f := startFilter(t, serve(t, peerID), true)
	c, err := newClient(masterID, peerOf(peerID, f.l.Addr().String()), peerID.PublicString(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s, err := c.Status(ctx)
		cancel()
		if err != nil || s.Name != "edge" {
			t.Fatalf("call %d: %+v %v", i, s, err)
		}
	}
	if n := f.stalled.Load(); n != 0 {
		t.Fatalf("%d connections looked like TLS", n)
	}
	// Each call is its own connection, and none stays open behind it.
	if n := f.conns.Load(); n != 5 {
		t.Fatalf("%d connections for 5 calls", n)
	}
	// Two first packets differ in size and share nothing to match on.
	f.mu.Lock()
	defer f.mu.Unlock()
	sizes := map[int]bool{}
	for _, b := range f.first {
		sizes[len(b)] = true
		if len(b) < veilNonceSize || bytes.Contains(b, []byte("neotunnel")) {
			t.Fatalf("first packet %x", b)
		}
	}
	if len(sizes) < 2 {
		t.Errorf("first packets all %v bytes", sizes)
	}
	if bytes.Equal(f.first[0][:veilNonceSize], f.first[1][:veilNonceSize]) {
		t.Error("the same nonce twice")
	}
}

// A node from before the veil is reached with plain TLS, and the route is
// remembered so the next call doesn't try the veil first.
func TestOlderNodeFallsBackToPlainTLS(t *testing.T) {
	masterID := newIdentity(t)
	peerID := newIdentity(t)
	edgeState(t, masterID.PublicString())
	addr := serveOld(t, peerID)
	c, _ := newClient(masterID, peerOf(peerID, addr), peerID.PublicString(), 5*time.Second)
	defer lastGood.Delete(peerID.PublicString())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if s, err := c.Status(ctx); err != nil || s.Name != "edge" {
		t.Fatalf("%+v %v", s, err)
	}
	if got := c.routes()[0]; !got.plain {
		t.Fatalf("next call starts with %+v", got)
	}
}

// The veil hides the shape and does nothing for trust: a wrong pin still fails
// whichever way it goes, and a stranger still isn't a master.
func TestVeilKeepsPinning(t *testing.T) {
	masterID := newIdentity(t)
	strangerID := newIdentity(t)
	peerID := newIdentity(t)
	edgeState(t, masterID.PublicString())
	addr := serve(t, peerID)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	wrong, _ := newClient(masterID, peerOf(peerID, addr), strangerID.PublicString(), 3*time.Second)
	if _, err := wrong.Status(ctx); err == nil {
		t.Fatal("reached a server that doesn't hold the pinned key")
	}
	stranger, _ := newClient(strangerID, peerOf(peerID, addr), peerID.PublicString(), 3*time.Second)
	if _, err := stranger.Status(ctx); err == nil {
		t.Fatal("a stranger read a peer's status")
	}
}

// A route that stopped answering isn't the first one tried any more.
func TestFailedRouteLosesItsPlace(t *testing.T) {
	masterID := newIdentity(t)
	peerID := newIdentity(t)
	edgeState(t, masterID.PublicString())
	s := &Server{ID: peerID, Probe: probe.NewServer(), Started: time.Now()}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	key := peerID.PublicString()
	defer lastGood.Delete(key)
	dead := route{"https://127.0.0.1:1", false}
	lastGood.Store(key, dead)
	c, _ := newClient(masterID, []string{"https://" + l.Addr().String(), dead.base}, key, 3*time.Second)
	if got := c.routes()[0]; got != dead {
		t.Fatalf("started with %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := c.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.routes()[0]; got.base == dead.base {
		t.Fatalf("still starts with the dead route: %+v", got)
	}
	l.Close()
}
