package relay

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHeaderV1(t *testing.T) {
	h := Header(1, false, netip.MustParseAddrPort("198.51.100.7:51234"), netip.MustParseAddrPort("203.0.113.1:443"))
	if string(h) != "PROXY TCP4 198.51.100.7 203.0.113.1 51234 443\r\n" {
		t.Errorf("v1 header %q", h)
	}
	h = Header(1, false, netip.MustParseAddrPort("[::ffff:198.51.100.7]:1"), netip.MustParseAddrPort("203.0.113.1:2"))
	if !strings.HasPrefix(string(h), "PROXY TCP4 198.51.100.7 ") {
		t.Errorf("mapped IPv4 client must be sent as TCP4: %q", h)
	}
}

func TestHeaderV2(t *testing.T) {
	h := Header(2, true, netip.MustParseAddrPort("198.51.100.7:51234"), netip.MustParseAddrPort("203.0.113.1:443"))
	want := append([]byte("\r\n\r\n\x00\r\nQUIT\n"), 0x21, 0x12, 0, 12,
		198, 51, 100, 7, 203, 0, 113, 1, 0xc8, 0x22, 0x01, 0xbb)
	if !bytes.Equal(h, want) {
		t.Errorf("v2 udp header\n got %x\nwant %x", h, want)
	}
	h = Header(2, false, netip.MustParseAddrPort("[2001:db8::7]:1"), netip.MustParseAddrPort("203.0.113.1:2"))
	if h[13] != 0x21 || len(h) != 16+36 {
		t.Errorf("mixed families must become TCP over IPv6: fam %x len %d", h[13], len(h))
	}
}

// A TCP relay delivers the header first, then the client's bytes, and
// passes the answer back.
func TestTCPRelay(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	got := make(chan string, 1)
	go func() {
		c, err := backend.Accept()
		if err != nil {
			return
		}
		r := bufio.NewReader(c)
		line, _ := r.ReadString('\n')
		body, _ := r.ReadString('!')
		got <- line + body
		c.Write([]byte("pong"))
		c.Close()
	}()

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	m := NewManager()
	defer m.Close()
	target := backend.Addr().(*net.TCPAddr).AddrPort()
	if errs := m.Sync([]Spec{{Proto: "tcp", Port: port, Target: target, Version: 1}}); len(errs) > 0 {
		t.Fatal(errs)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("ping!"))
	reply, _ := io.ReadAll(c)
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "PROXY TCP4 127.0.0.1 127.0.0.1 ") || !strings.HasSuffix(s, "\r\nping!") {
			t.Errorf("backend got %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backend got nothing")
	}
	if string(reply) != "pong" {
		t.Errorf("client got %q", reply)
	}
}
