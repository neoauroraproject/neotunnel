package relay

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"neotunnel/internal/config"
)

func freeTCP(t *testing.T) uint16 {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// relayPair runs an exit (side 1) allowing ports and an entry (side 0)
// listening on a free port that relays to target, and returns the entry
// port.
func relayPair(t *testing.T, secret, exitSecret, cipher string, version int, target uint16, ports ...uint16) uint16 {
	t.Helper()
	exitPort := freeTCP(t)
	exit := newManager(int(exitPort))
	t.Cleanup(exit.Close)
	if err := exit.SyncExits([]Exit{{Tunnel: "t1", Secret: exitSecret, Side: 1, Ports: ports}}); err != nil {
		t.Fatal(err)
	}
	entry := NewManager()
	t.Cleanup(entry.Close)
	port := freeTCP(t)
	via := Via{Tunnel: "t1", Secret: secret, Side: 0, Exit: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), exitPort), Cipher: cipher, Pool: 2}
	spec := Spec{Proto: "tcp", Port: port, Target: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), target), Version: version, Via: via}
	if errs := entry.Sync([]Spec{spec}); len(errs) > 0 {
		t.Fatal(errs)
	}
	return port
}

// hashServer reads everything, answers with its SHA-256 (after an optional
// PROXY v1 line it reports first), then closes.
func hashServer(t *testing.T, proxyLine bool) (uint16, chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	lines := make(chan string, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				if proxyLine {
					l, _ := r.ReadString('\n')
					lines <- l
				}
				h := sha256.New()
				io.Copy(h, r)
				c.Write(h.Sum(nil))
			}()
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port), lines
}

func roundTrip(t *testing.T, port uint16, data []byte) []byte {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	go func() {
		c.Write(data)
		c.(*net.TCPConn).CloseWrite() // the server answers after our half-close
	}()
	got, _ := io.ReadAll(c)
	return got
}

func TestStreamRelay(t *testing.T) {
	secret := config.NewSecret()
	data := make([]byte, 3<<20)
	rand.Read(data)
	want := sha256.Sum256(data)
	for _, cipher := range []string{"", "aes-gcm-128", "chacha20-poly1305"} {
		t.Run("cipher="+cipher, func(t *testing.T) {
			back, lines := hashServer(t, true)
			port := relayPair(t, secret, secret, cipher, 1, back, back)
			// Several connections, more than the pool holds.
			for i := 0; i < 4; i++ {
				got := roundTrip(t, port, data)
				if !bytes.Equal(got, want[:]) {
					t.Fatalf("connection %d: the service got different bytes (%d byte answer)", i, len(got))
				}
				l := <-lines
				if !strings.HasPrefix(l, "PROXY TCP4 127.0.0.1 127.0.0.1 ") || !strings.HasSuffix(l, " "+strconv.Itoa(int(port))+"\r\n") {
					t.Fatalf("PROXY header %q", l)
				}
			}
		})
	}
}

// Without a PROXY version the service gets the user's bytes only.
func TestStreamRelayNoHeader(t *testing.T) {
	secret := config.NewSecret()
	back, _ := hashServer(t, false)
	port := relayPair(t, secret, secret, "", 0, back, back)
	want := sha256.Sum256([]byte("hello"))
	if got := roundTrip(t, port, []byte("hello")); !bytes.Equal(got, want[:]) {
		t.Fatalf("got %x", got)
	}
}

// An entry without the tunnel's secret gets nothing through, and the exit
// never connects to a port no forward names.
func TestStreamRelayRefuses(t *testing.T) {
	back, lines := hashServer(t, true)
	port := relayPair(t, config.NewSecret(), config.NewSecret(), "", 1, back, back)
	if got := roundTrip(t, port, []byte("x")); len(got) != 0 {
		t.Errorf("wrong secret: got %q", got)
	}
	secret := config.NewSecret()
	port = relayPair(t, secret, secret, "", 1, back, back+1)
	if got := roundTrip(t, port, []byte("x")); len(got) != 0 {
		t.Errorf("port not allowed: got %q", got)
	}
	select {
	case l := <-lines:
		t.Errorf("the service was reached: %q", l)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRequestRoundTrip(t *testing.T) {
	q := request{port: 443, version: 2, src: netip.MustParseAddrPort("198.51.100.7:51234"), dst: netip.MustParseAddrPort("[2001:db8::1]:443")}
	got, err := readRequest(bytes.NewReader(q.marshal()))
	if err != nil || got != q {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := readRequest(bytes.NewReader([]byte{1, 187, 9})); err == nil {
		t.Error("bad version accepted")
	}
}
