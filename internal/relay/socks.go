package relay

// A socks forward is a proxy for programs on the entry node: they connect
// to Listen:Port with SOCKS5 or HTTP (one port, told apart by the first
// byte), name a host and port, and the connection goes like a relay one to
// the far node (through the tunnel), which connects to that host and port.
// So the programs reach the internet from the far node's address, and
// names are resolved there too.

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SOCKS5 reply codes, also the exit's status byte.
const (
	repOK          = 0
	repFailure     = 1
	repNotAllowed  = 2
	repNetUnreach  = 3
	repHostUnreach = 4
	repRefused     = 5
	repTimeout     = 6
	repCmd         = 7
	repAddrType    = 8
)

// handshakeWait bounds how long a program may take to say where to go.
const handshakeWait = 30 * time.Second

func (m *Manager) serveSocks(s Spec) (*server, error) {
	p := m.pools[s.Via]
	if p == nil {
		var err error
		if p, err = newPool(s.Via); err != nil {
			return nil, err
		}
		m.pools[s.Via] = p
	}
	return m.listenTCP(s, func(c *net.TCPConn) { m.handleSocks(s, p, c) })
}

func (m *Manager) handleSocks(s Spec, p *pool, c *net.TCPConn) {
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	defer c.Close()
	c.SetDeadline(time.Now().Add(handshakeWait))
	br := bufio.NewReaderSize(c, 4096)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	var up net.Conn
	if first[0] == 5 {
		up = socks5(s, p, br, c)
	} else {
		up = httpProxy(s, p, br, c)
	}
	if up == nil {
		return
	}
	defer up.Close()
	c.SetDeadline(time.Time{})
	// Bytes the program sent right after its request are already read.
	if n := br.Buffered(); n > 0 {
		b, _ := br.Peek(n)
		if _, err := up.Write(b); err != nil {
			return
		}
	}
	pipe(c, up.(halfConn))
}

// connect asks the far node for a connection to host:port.
func connect(p *pool, host string, port uint16) (net.Conn, byte) {
	up, err := p.get()
	if err != nil {
		return nil, repNetUnreach
	}
	if _, err := up.Write(request{port: port, host: host}.marshal()); err != nil {
		up.Close()
		return nil, repFailure
	}
	up.SetReadDeadline(time.Now().Add(dialTimeout + 5*time.Second))
	var st [1]byte
	if _, err := io.ReadFull(up, st[:]); err != nil {
		// An exit too old to know proxies closes the connection.
		up.Close()
		return nil, repFailure
	}
	up.SetReadDeadline(time.Time{})
	if st[0] != repOK {
		up.Close()
		return nil, st[0]
	}
	return up, repOK
}

func checkAuth(want, user, pass string) bool {
	u, p, _ := strings.Cut(want, ":")
	return subtle.ConstantTimeCompare([]byte(u), []byte(user))&subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
}

// socks5 runs the SOCKS5 handshake (RFC 1928, CONNECT only; RFC 1929
// password when Auth is set) and returns the far connection, or nil.
func socks5(s Spec, p *pool, r *bufio.Reader, c net.Conn) net.Conn {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil
	}
	methods := make([]byte, h[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		return nil
	}
	want := byte(0)
	if s.Auth != "" {
		want = 2
	}
	if !strings.ContainsRune(string(methods), rune(want)) {
		c.Write([]byte{5, 0xff})
		return nil
	}
	if _, err := c.Write([]byte{5, want}); err != nil {
		return nil
	}
	if want == 2 {
		user, pass, err := readUserPass(r)
		if err != nil {
			return nil
		}
		if !checkAuth(s.Auth, user, pass) {
			c.Write([]byte{1, 1})
			return nil
		}
		if _, err := c.Write([]byte{1, 0}); err != nil {
			return nil
		}
	}
	reply := func(code byte) { c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}) }
	var q [4]byte
	if _, err := io.ReadFull(r, q[:]); err != nil {
		return nil
	}
	if q[1] != 1 { // CONNECT
		reply(repCmd)
		return nil
	}
	var host string
	switch q[3] {
	case 1, 4:
		b := make([]byte, 4)
		if q[3] == 4 {
			b = make([]byte, 16)
		}
		if _, err := io.ReadFull(r, b); err != nil {
			return nil
		}
		a, _ := netip.AddrFromSlice(b)
		host = a.Unmap().String()
	case 3:
		n, err := r.ReadByte()
		if err != nil || n == 0 {
			return nil
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil
		}
		host = string(b)
	default:
		reply(repAddrType)
		return nil
	}
	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return nil
	}
	up, code := connect(p, host, binary.BigEndian.Uint16(pb[:]))
	reply(code)
	return up
}

func readUserPass(r *bufio.Reader) (string, string, error) {
	field := func() (string, error) {
		n, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		return string(b), err
	}
	if v, err := r.ReadByte(); err != nil || v != 1 {
		return "", "", errors.New("bad SOCKS5 password version")
	}
	u, err := field()
	if err != nil {
		return "", "", err
	}
	pw, err := field()
	return u, pw, err
}

// httpProxy serves one HTTP proxy request: CONNECT (HTTPS and anything
// else), or a plain http:// request, which is sent on with
// "Connection: close" so the next one comes on a new connection. It
// returns the far connection for CONNECT, or nil when it is done.
func httpProxy(s Spec, p *pool, r *bufio.Reader, c net.Conn) net.Conn {
	req, err := http.ReadRequest(r)
	if err != nil {
		return nil
	}
	status := func(code int, extra string) {
		fmt.Fprintf(c, "HTTP/1.1 %d %s\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", code, http.StatusText(code), extra)
	}
	if s.Auth != "" && !httpAuth(s.Auth, req.Header.Get("Proxy-Authorization")) {
		status(http.StatusProxyAuthRequired, "Proxy-Authenticate: Basic realm=\"NeoTunnel\"\r\n")
		return nil
	}
	target := req.Host
	if req.Method != http.MethodConnect {
		if req.URL.Scheme != "http" || req.URL.Host == "" {
			status(http.StatusBadRequest, "")
			return nil
		}
		target = req.URL.Host
		if req.URL.Port() == "" {
			target = net.JoinHostPort(req.URL.Hostname(), "80")
		}
	}
	host, ps, err := net.SplitHostPort(target)
	port, perr := strconv.ParseUint(ps, 10, 16)
	if err != nil || perr != nil || host == "" || len(host) > 255 {
		status(http.StatusBadRequest, "")
		return nil
	}
	up, code := connect(p, host, uint16(port))
	if up == nil {
		status(httpStatus(code), "")
		return nil
	}
	if req.Method == http.MethodConnect {
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			up.Close()
			return nil
		}
		return up
	}
	defer up.Close()
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Close = true
	c.SetDeadline(time.Time{})
	if err := req.Write(up); err != nil {
		return nil
	}
	io.Copy(c, up)
	return nil
}

func httpAuth(want, header string) bool {
	b64, ok := strings.CutPrefix(header, "Basic ")
	if !ok {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return false
	}
	u, pw, _ := strings.Cut(string(raw), ":")
	return checkAuth(want, u, pw)
}

func httpStatus(code byte) int {
	switch code {
	case repNotAllowed:
		return http.StatusForbidden
	case repTimeout:
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// ---------- exit ----------

// egress connects a proxy's connection to the internet from this node.
func (m *Manager) egress(x *exitServer, tunnel string, q request, sc net.Conn) {
	if e, ok := (*x.exits.Load())[tunnel]; !ok || !e.Egress {
		sc.Write([]byte{repNotAllowed})
		return
	}
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	up, code := dialOut(q.host, q.port)
	if _, err := sc.Write([]byte{code}); err != nil || up == nil {
		if up != nil {
			up.Close()
		}
		return
	}
	defer up.Close()
	pipe(sc.(halfConn), up)
}

// dialOut connects to host:port, resolving the name here, and never to an
// address of this node's own or private networks.
func dialOut(host string, port uint16) (*net.TCPConn, byte) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host); err != nil || len(addrs) == 0 {
		return nil, repHostUnreach
	}
	code := byte(repNotAllowed)
	for _, a := range addrs {
		a = a.Unmap()
		if !destAllowed(a) {
			continue
		}
		d := net.Dialer{KeepAlive: 30 * time.Second}
		c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(a, port).String())
		if err == nil {
			return c.(*net.TCPConn), repOK
		}
		code = dialCode(err)
	}
	return nil, code
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// destAllowed decides where a proxy may connect (tests reach loopback).
var destAllowed = public

// public reports whether a proxy may connect to a: not loopback, link-local,
// multicast, private or shared (CGNAT) address space.
func public(a netip.Addr) bool {
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

func dialCode(err error) byte {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return repRefused
	case errors.Is(err, syscall.ENETUNREACH):
		return repNetUnreach
	case errors.Is(err, syscall.EHOSTUNREACH):
		return repHostUnreach
	case errors.As(err, &ne) && ne.Timeout():
		return repTimeout
	}
	return repFailure
}
