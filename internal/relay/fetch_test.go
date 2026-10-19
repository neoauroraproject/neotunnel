package relay

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"
)

// clientHello is what a real TLS client sends first for name.
func clientHello(t *testing.T, name string) []byte {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go tls.Client(a, &tls.Config{ServerName: name, InsecureSkipVerify: name == ""}).Handshake()
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	var buf bytes.Buffer
	io.CopyN(&buf, b, 5)
	n := int(buf.Bytes()[3])<<8 | int(buf.Bytes()[4])
	io.CopyN(&buf, b, int64(n))
	return buf.Bytes()
}

func TestSniffTLS(t *testing.T) {
	hello := clientHello(t, "Objects.GitHubusercontent.com")
	// Arriving in small pieces, as it may over TCP.
	r, w := net.Pipe()
	go func() {
		for i := 0; i < len(hello); i += 100 {
			w.Write(hello[i:min(i+100, len(hello))])
		}
	}()
	r.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, host := sniff(r)
	if host != "objects.githubusercontent.com" {
		t.Fatalf("host %q", host)
	}
	if !bytes.Equal(got, hello) {
		t.Fatal("sniff must return exactly the bytes it read")
	}
	if h, done := siteOf(clientHello(t, "")); !done || h != "" {
		t.Errorf("no server name: %q %v", h, done)
	}
}

func TestSniffHTTP(t *testing.T) {
	for head, want := range map[string]string{
		"GET /ubuntu/dists/noble/InRelease HTTP/1.1\r\nHost: Archive.Ubuntu.com\r\nUser-Agent: apt\r\n\r\n": "archive.ubuntu.com",
		"GET / HTTP/1.1\r\nhost: example.org:8080\r\n\r\n":                                                  "example.org",
		"GET / HTTP/1.1\r\nHost: 1.2.3.4\r\n\r\n":                                                           "",
	} {
		if h, done := siteOf([]byte(head)); !done || h != want {
			t.Errorf("%q: %q %v, want %q", head, h, done, want)
		}
	}
	if _, done := siteOf([]byte("GET / HTTP/1.1\r\nHost: a")); done {
		t.Error("an unfinished request head must wait for more")
	}
	if h, done := siteOf([]byte{0x00, 0x01}); !done || h != "" {
		t.Error("an unknown protocol has no site")
	}
}

func TestMatchSite(t *testing.T) {
	sites := []string{"github.com", "archive.ubuntu.com"}
	for host, want := range map[string]bool{
		"github.com": true, "codeload.github.com": true, "ir.archive.ubuntu.com": true,
		"notgithub.com": false, "github.com.evil.net": false, "ubuntu.com": false, "": false,
	} {
		if matchSite(host, sites) != want {
			t.Errorf("%q: want %v", host, want)
		}
	}
}
