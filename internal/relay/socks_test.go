package relay

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strings"
	"testing"

	"neotunnel/internal/config"
)

// socksPair runs an exit (side 1) with egress and a proxy entry (side 0) on
// loopback, and returns the proxy's port.
func socksPair(t *testing.T, cipher, auth string, egress bool) uint16 {
	t.Helper()
	destAllowed = func(a netip.Addr) bool { return a.IsLoopback() }
	t.Cleanup(func() { destAllowed = public })
	secret := config.NewSecret()
	exitPort := freeTCP(t)
	exit := newManager(int(exitPort))
	t.Cleanup(exit.Close)
	if err := exit.SyncExits([]Exit{{Tunnel: "t1", Secret: secret, Side: 1, Egress: egress}}); err != nil {
		t.Fatal(err)
	}
	entry := NewManager()
	t.Cleanup(entry.Close)
	port := freeTCP(t)
	lo := netip.MustParseAddr("127.0.0.1")
	via := Via{Tunnel: "t1", Secret: secret, Side: 0, Exit: netip.AddrPortFrom(lo, exitPort), Cipher: cipher, Pool: 2}
	if errs := entry.Sync([]Spec{{Proto: "tcp", Port: port, Via: via, Socks: true, Listen: lo, Auth: auth}}); len(errs) > 0 {
		t.Fatal(errs)
	}
	return port
}

// echoServer answers every connection with "hi " and what it got, then
// closes.
func echoServer(t *testing.T) uint16 {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(io.LimitReader(c, 5))
				c.Write(append([]byte("hi "), b...))
			}()
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// socksDial runs a SOCKS5 CONNECT to host:port and returns the reply code.
func socksDial(t *testing.T, proxy uint16, user, pass, host string, port uint16) (net.Conn, byte) {
	t.Helper()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	method := byte(0)
	if user != "" {
		method = 2
	}
	c.Write([]byte{5, 1, method})
	var r [2]byte
	if _, err := io.ReadFull(c, r[:]); err != nil || r[1] != method {
		return c, 0xff
	}
	if user != "" {
		c.Write(append(append(append([]byte{1, byte(len(user))}, user...), byte(len(pass))), pass...))
		if _, err := io.ReadFull(c, r[:]); err != nil || r[1] != 0 {
			return c, 0xfe
		}
	}
	req := append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	req = append(req, byte(port>>8), byte(port))
	c.Write(req)
	var rep [10]byte
	if _, err := io.ReadFull(c, rep[:]); err != nil {
		t.Fatal(err)
	}
	return c, rep[1]
}

func TestSocks5(t *testing.T) {
	for _, cipher := range []string{"", "chacha20-poly1305"} {
		t.Run("cipher="+cipher, func(t *testing.T) {
			echo := echoServer(t)
			proxy := socksPair(t, cipher, "", true)
			c, code := socksDial(t, proxy, "", "", "localhost", echo)
			if code != repOK {
				t.Fatalf("reply %d", code)
			}
			c.Write([]byte("hello"))
			got, _ := io.ReadAll(c)
			if string(got) != "hi hello" {
				t.Fatalf("got %q", got)
			}
			// Nothing listens here: refused (Windows reports it otherwise).
			if _, code := socksDial(t, proxy, "", "", "127.0.0.1", freeTCP(t)); code != repRefused && (runtime.GOOS == "linux" || code == repOK) {
				t.Fatalf("closed port: reply %d", code)
			}
		})
	}
}

func TestSocks5Password(t *testing.T) {
	echo := echoServer(t)
	proxy := socksPair(t, "", "ali:s3cret", true)
	if _, code := socksDial(t, proxy, "ali", "wrong", "localhost", echo); code != 0xfe {
		t.Fatalf("wrong password: %d", code)
	}
	if _, code := socksDial(t, proxy, "", "", "localhost", echo); code != 0xff {
		t.Fatalf("no password: %d", code)
	}
	if _, code := socksDial(t, proxy, "ali", "s3cret", "localhost", echo); code != repOK {
		t.Fatalf("right password: %d", code)
	}
}

// The exit only connects out for tunnels that run a proxy, and never to
// addresses the policy forbids.
func TestSocksExitRefuses(t *testing.T) {
	echo := echoServer(t)
	proxy := socksPair(t, "", "", false)
	if _, code := socksDial(t, proxy, "", "", "localhost", echo); code != repNotAllowed {
		t.Fatalf("no egress: reply %d", code)
	}
	proxy = socksPair(t, "", "", true)
	destAllowed = public
	if _, code := socksDial(t, proxy, "", "", "127.0.0.1", echo); code != repNotAllowed {
		t.Fatalf("loopback: reply %d", code)
	}
	for _, a := range []string{"10.1.2.3", "192.168.1.1", "100.64.0.1", "169.254.169.254", "::1", "fd00::1", "0.0.0.0"} {
		if public(netip.MustParseAddr(a)) {
			t.Errorf("%s counts as public", a)
		}
	}
	if !public(netip.MustParseAddr("149.154.167.50")) {
		t.Error("a public address is refused")
	}
}

func TestHTTPProxy(t *testing.T) {
	echo := echoServer(t)
	proxy := socksPair(t, "aes-gcm-128", "u:p", true)
	dial := func() (net.Conn, *bufio.Reader) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c, bufio.NewReader(c)
	}
	auth := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("u:p")) + "\r\n"

	c, r := dial()
	fmt.Fprintf(c, "CONNECT localhost:%d HTTP/1.1\r\nHost: localhost\r\n\r\n", echo)
	if resp, err := http.ReadResponse(r, nil); err != nil || resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("no password: %v %v", resp, err)
	}

	c, r = dial()
	fmt.Fprintf(c, "CONNECT localhost:%d HTTP/1.1\r\nHost: localhost\r\n%s\r\nhello", echo, auth)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "hi hello" {
		t.Fatalf("CONNECT got %q", got)
	}

	// A plain request goes on in origin form, without proxy headers.
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		fmt.Fprintf(w, "%s %s auth=%q", q.Method, q.RequestURI, q.Header.Get("Proxy-Authorization"))
	})}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	c, r = dial()
	fmt.Fprintf(c, "GET http://%s/x?y=1 HTTP/1.1\r\nHost: %s\r\n%s\r\n", ln.Addr(), ln.Addr(), auth)
	resp, err = http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), `GET /x?y=1 auth=""`) {
		t.Fatalf("plain request: %q", body)
	}
}
