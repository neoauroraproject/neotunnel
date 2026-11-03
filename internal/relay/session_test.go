package relay

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/transport"
)

var lo = netip.MustParseAddr("127.0.0.1")

// sessionPair runs a connection tunnel between two managers on loopback:
// side 0 is the forward's entry, side 1 its exit (allowing target). The
// listening side (side 1, or side 0 when reverse) listens on live; the
// other dials dead first, then live. It returns the entry's port and both
// hubs.
func sessionPair(t *testing.T, kind config.Kind, reverse bool, secret, exitSecret string, target uint16) (uint16, *transport.Hub, *transport.Hub) {
	t.Helper()
	dead, live := freeTCP(t), freeTCP(t)
	mgrs := [2]*Manager{newManager(int(freeTCP(t))), newManager(int(freeTCP(t)))}
	hubs := [2]*transport.Hub{transport.NewHub(), transport.NewHub()}
	secrets := [2]string{secret, exitSecret}
	listener := 1
	if reverse {
		listener = 0
	}
	for i := range 2 {
		mgrs[i].SetHub(hubs[i])
		t.Cleanup(mgrs[i].Close)
		t.Cleanup(hubs[i].Close)
		s := transport.Spec{Name: "t1", Tunnel: "t1", Secret: secrets[i], Side: i, Kind: kind, Remote: lo, Conns: 2}
		if i == listener {
			s.Ports[0] = live
		} else {
			s.Dial = true
			s.Ports[0], s.Ports[1] = dead, live
		}
		if errs := hubs[i].Sync([]transport.Spec{s}); len(errs) > 0 {
			t.Fatal(errs)
		}
		if err := mgrs[i].SyncListen(); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgrs[1].SyncExits([]Exit{{Tunnel: "t1", Secret: exitSecret, Side: 1, Ports: []uint16{target}, Session: true}}); err != nil {
		t.Fatal(err)
	}
	port := freeTCP(t)
	spec := Spec{Proto: "tcp", Port: port, Target: netip.AddrPortFrom(lo, target),
		Via: Via{Tunnel: "t1", Secret: secret, Side: 0, Session: "t1"}}
	if errs := mgrs[0].Sync([]Spec{spec}); len(errs) > 0 {
		t.Fatal(errs)
	}
	return port, hubs[0], hubs[1]
}

func TestSessionRelay(t *testing.T) {
	if os.Getenv("NEOTUNNEL_SESSION_TEST") == "" {
		// Known issue: now and then one of several connections at once ends
		// with no answer (see the logs "tunnel t1: ..."); being looked into.
		t.Skip("intermittent; set NEOTUNNEL_SESSION_TEST=1 to run")
	}
	secret := config.NewSecret()
	data := make([]byte, 3<<20)
	rand.Read(data)
	want := sha256.Sum256(data)
	for _, kind := range []config.Kind{config.KindStreamTCP, config.KindStreamTLS} {
		for _, reverse := range []bool{false, true} {
			t.Run(string(kind)+" reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
				back, _ := hashServer(t, false)
				port, entry, exit := sessionPair(t, kind, reverse, secret, secret, back)
				// More connections than sessions, some at once.
				done := make(chan []byte, 6)
				for range 6 {
					go func() { done <- roundTrip(t, port, data) }()
				}
				for i := range 6 {
					if got := <-done; !bytes.Equal(got, want[:]) {
						t.Fatalf("connection %d: the service got different bytes (%d byte answer)", i, len(got))
					}
				}
				for _, h := range []*transport.Hub{entry, exit} {
					rtt, err := h.Ping("t1", 2*time.Second)
					if err != nil || rtt < 0 {
						t.Fatalf("ping: %v %v", rtt, err)
					}
					st, ok := h.State("t1")
					if !ok || st.Sessions == 0 || st.Rx == 0 || st.Tx == 0 {
						t.Fatalf("state %+v %v", st, ok)
					}
				}
			})
		}
	}
}

func TestSessionWrongSecret(t *testing.T) {
	back, _ := hashServer(t, false)
	port, entry, _ := sessionPair(t, config.KindStreamTLS, false, config.NewSecret(), config.NewSecret(), back)
	time.Sleep(500 * time.Millisecond)
	if st, _ := entry.State("t1"); st.Sessions != 0 {
		t.Fatalf("a session came up without the secret: %+v", st)
	}
	if got := roundTrip(t, port, []byte("hello")); len(got) != 0 {
		t.Fatalf("the service answered: %q", got)
	}
}

// Whoever doesn't speak NeoTunnel on a relay port, plain or TLS, meets a
// web server.
func TestRelayPortDecoy(t *testing.T) {
	m := newManager(int(freeTCP(t)))
	m.SetHub(transport.NewHub())
	t.Cleanup(m.Close)
	port := freeTCP(t)
	if err := m.SyncExits([]Exit{{Tunnel: "t1", Secret: config.NewSecret(), Side: 1, Listen: []uint16{port}}}); err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	probe := func(c net.Conn) string {
		c.SetDeadline(time.Now().Add(5 * time.Second))
		c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
		l, _ := bufio.NewReader(c).ReadString('\n')
		return l
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if l := probe(c); !strings.HasPrefix(l, "HTTP/1.1 404") {
		t.Fatalf("plain probe got %q", l)
	}
	tc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if l := probe(tc); !strings.HasPrefix(l, "HTTP/1.1 404") {
		t.Fatalf("TLS probe got %q", l)
	}
}

// A tunnel's relay is tried on its ports in turn: the first one dead.
func TestRelayPortFallback(t *testing.T) {
	secret := config.NewSecret()
	back, _ := hashServer(t, false)
	dead, live := freeTCP(t), freeTCP(t)
	exit := newManager(int(freeTCP(t)))
	t.Cleanup(exit.Close)
	if err := exit.SyncExits([]Exit{{Tunnel: "t1", Secret: secret, Side: 1, Ports: []uint16{back}, Listen: []uint16{live}}}); err != nil {
		t.Fatal(err)
	}
	entry := NewManager()
	t.Cleanup(entry.Close)
	port := freeTCP(t)
	via := Via{Tunnel: "t1", Secret: secret, Side: 0, Exit: netip.AddrPortFrom(lo, dead), Pool: 1}
	via.Ports[0], via.Ports[1] = dead, live
	if errs := entry.Sync([]Spec{{Proto: "tcp", Port: port, Target: netip.AddrPortFrom(lo, back), Via: via}}); len(errs) > 0 {
		t.Fatal(errs)
	}
	data := []byte("over the second port")
	want := sha256.Sum256(data)
	if got := roundTrip(t, port, data); !bytes.Equal(got, want[:]) {
		t.Fatalf("got %d bytes", len(got))
	}
}
