package relay

// Downloads through a tunnel: nftables redirects this server's own outgoing
// HTTP and HTTPS connections to a local listener (config.FetchPort). The
// listener asks the kernel where each was going, reads the site's name from
// the first bytes the program sends (the TLS ClientHello's server name, or
// the HTTP Host header), and
//
//   - for a listed site, asks the tunnel's far node to connect to that name
//     and port, exactly like a socks forward's connection: the name is
//     resolved there, so a wrong answer from DNS here doesn't matter;
//   - for anything else, connects to the original address itself.
//
// Then it sends on the bytes it read and copies both ways. The listener's own
// direct connections carry FetchMark, which the redirect skips.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FetchMark is the socket mark of connections the daemon makes straight to
// the internet, which the downloads redirect must leave alone.
const FetchMark = 0x4e54f001

const (
	sniffWait = 5 * time.Second
	sniffMax  = 16 << 10
)

// fetcher is the running downloads listener.
type fetcher struct {
	pool   *pool
	sites  []string
	strict bool
}

var fetchNow atomic.Pointer[fetcher]

func (m *Manager) serveFetch(s Spec) (*server, error) {
	p := m.pools[s.Via]
	if p == nil {
		var err error
		if p, err = newPool(s.Via); err != nil {
			return nil, err
		}
		m.pools[s.Via] = p
	}
	f := &fetcher{pool: p, sites: strings.Split(s.FetchSites, ","), strict: s.FetchStrict}
	var lns []*net.TCPListener
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback} {
		ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: ip, Port: int(s.Port)})
		if err != nil {
			if ip.To4() == nil {
				continue // a server without IPv6
			}
			return nil, err
		}
		lns = append(lns, ln)
		go func() {
			for {
				c, err := ln.AcceptTCP()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					time.Sleep(100 * time.Millisecond)
					continue
				}
				go m.handleFetch(f, c)
			}
		}()
	}
	fetchNow.Store(f)
	return &server{spec: s, close: func() {
		fetchNow.CompareAndSwap(f, nil)
		for _, ln := range lns {
			ln.Close()
		}
	}}, nil
}

func (m *Manager) handleFetch(f *fetcher, c *net.TCPConn) {
	m.Conns.Add(1)
	defer m.Conns.Add(-1)
	defer c.Close()
	dst, ok := originalDst(c)
	if !ok || dst == c.LocalAddr().(*net.TCPAddr).AddrPort() {
		return // not redirected: someone connected to the listener itself
	}
	c.SetReadDeadline(time.Now().Add(sniffWait))
	first, host := sniff(c)
	c.SetReadDeadline(time.Time{})
	var up net.Conn
	via := "direct"
	if host != "" && matchSite(host, f.sites) {
		var code byte
		up, code = connect(f.pool, host, dst.Port())
		via = "tunnel"
		if up == nil {
			fetchLog.add(host, "failed", fmt.Sprintf("tunnel: %s", replyText(code)))
			if f.strict {
				return
			}
			via = "direct"
		}
	}
	if up == nil {
		d, err := dialDirect(context.Background(), dst.String())
		if err != nil {
			if host != "" {
				fetchLog.add(host, "failed", err.Error())
			}
			return
		}
		up = d
	}
	defer up.Close()
	if host != "" {
		fetchLog.add(host, via, "")
	}
	if len(first) > 0 {
		if _, err := up.Write(first); err != nil {
			return
		}
	}
	pipe(c, up.(halfConn))
}

// sniff reads the start of a connection until the site's name shows: it
// returns what it read and the name ("" if none came within sniffMax bytes
// or the deadline).
func sniff(r io.Reader) ([]byte, string) {
	buf := make([]byte, 0, 4096)
	for len(buf) < sniffMax {
		if len(buf) == cap(buf) {
			buf = append(buf, make([]byte, len(buf))...)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if host, done := siteOf(buf); done {
			return buf, host
		}
		if err != nil {
			break
		}
	}
	return buf, ""
}

// siteOf finds the site's name in the first bytes of a connection; done is
// false while more bytes could still tell.
func siteOf(b []byte) (host string, done bool) {
	if len(b) == 0 {
		return "", false
	}
	if b[0] == 0x16 { // TLS handshake record
		if len(b) < 5 {
			return "", false
		}
		n := 5 + int(binary.BigEndian.Uint16(b[3:5]))
		if len(b) < n {
			return "", false
		}
		return ServerName(b[5:n]), true
	}
	if b[0] < 'A' || b[0] > 'Z' { // not an HTTP method either
		return "", true
	}
	end := strings.Index(string(b), "\r\n\r\n")
	if end < 0 {
		return "", false
	}
	return HTTPHost(b[:end]), true
}

// ServerName is the server name a TLS ClientHello (the handshake message,
// without its record header) asks for, or "".
func ServerName(h []byte) string {
	// type(1) length(3) version(2) random(32)
	if len(h) < 38 || h[0] != 1 {
		return ""
	}
	p := h[38:]
	skip := func(lenBytes int) bool {
		if len(p) < lenBytes {
			return false
		}
		n := 0
		for _, c := range p[:lenBytes] {
			n = n<<8 | int(c)
		}
		if len(p) < lenBytes+n {
			return false
		}
		p = p[lenBytes+n:]
		return true
	}
	if !skip(1) || !skip(2) || !skip(1) { // session id, cipher suites, compression
		return ""
	}
	if len(p) < 2 {
		return ""
	}
	ext := p[2:]
	if n := int(binary.BigEndian.Uint16(p)); n < len(ext) {
		ext = ext[:n]
	}
	for len(ext) >= 4 {
		typ, n := binary.BigEndian.Uint16(ext), int(binary.BigEndian.Uint16(ext[2:]))
		if len(ext) < 4+n {
			return ""
		}
		data := ext[4 : 4+n]
		ext = ext[4+n:]
		if typ != 0 { // server_name
			continue
		}
		// list length(2), then entries: type(1) length(2) name
		if len(data) < 2 {
			return ""
		}
		data = data[2:]
		for len(data) >= 3 {
			l := int(binary.BigEndian.Uint16(data[1:]))
			if len(data) < 3+l {
				return ""
			}
			if data[0] == 0 {
				return cleanHost(string(data[3 : 3+l]))
			}
			data = data[3+l:]
		}
		return ""
	}
	return ""
}

// HTTPHost is the host an HTTP request's head names, without its port.
func HTTPHost(head []byte) string {
	for _, line := range strings.Split(string(head), "\r\n")[1:] {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "host") {
			v = strings.TrimSpace(v)
			if h, _, err := net.SplitHostPort(v); err == nil {
				v = h
			}
			return cleanHost(v)
		}
	}
	return ""
}

func cleanHost(h string) string {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if _, err := netip.ParseAddr(h); err == nil {
		return "" // an address names no site
	}
	return h
}

// matchSite reports whether host is one of the sites or under one.
func matchSite(host string, sites []string) bool {
	for _, s := range sites {
		if s != "" && (host == s || strings.HasSuffix(host, "."+s)) {
			return true
		}
	}
	return false
}

// dialDirect connects past the downloads redirect.
func dialDirect(ctx context.Context, addr string) (*net.TCPConn, error) {
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, Control: markSocket}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

// FetchDial connects to host:port the way the downloads would: through the
// tunnel for a listed site (and then, unless strict, directly if the tunnel
// fails), directly otherwise. With force "tunnel" or "direct" it takes only
// that way, for a test. It is for the daemon's own requests (Let's Encrypt,
// the panel's test), which the redirect may not catch.
func FetchDial(ctx context.Context, addr, force string) (net.Conn, error) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(ps, 10, 16)
	if err != nil {
		return nil, err
	}
	f := fetchNow.Load()
	tunnel := force == "tunnel" || (force == "" && f != nil && matchSite(cleanHost(host), f.sites))
	if tunnel {
		if f == nil {
			return nil, errors.New("downloads through the tunnel are off")
		}
		type result struct {
			c    net.Conn
			code byte
		}
		ch := make(chan result, 1)
		go func() {
			c, code := connect(f.pool, host, uint16(port))
			ch <- result{c, code}
		}()
		select {
		case r := <-ch:
			if r.c != nil {
				return r.c, nil
			}
			err = fmt.Errorf("through the tunnel: %s", replyText(r.code))
		case <-ctx.Done():
			go func() {
				if r := <-ch; r.c != nil {
					r.c.Close()
				}
			}()
			return nil, ctx.Err()
		}
		if force == "tunnel" || f.strict {
			return nil, err
		}
	}
	return dialDirect(ctx, addr)
}

func replyText(code byte) string {
	switch code {
	case repNotAllowed:
		return "the far server does not allow it (update it or sync it)"
	case repNetUnreach:
		return "the far server can't be reached"
	case repHostUnreach:
		return "the site's name doesn't resolve on the far server"
	case repRefused:
		return "connection refused"
	case repTimeout:
		return "timed out"
	}
	return "failed"
}

// ---------- status ----------

// FetchEntry is one recent download connection.
type FetchEntry struct {
	Host string `json:"host"`
	Via  string `json:"via"` // tunnel, direct or failed
	Err  string `json:"error,omitempty"`
	At   int64  `json:"at"` // unix ms
}

// FetchStats is what the downloads listener has done since the daemon
// started.
type FetchStats struct {
	Running bool         `json:"running"`
	Tunnel  uint64       `json:"tunnel"`
	Direct  uint64       `json:"direct"`
	Failed  uint64       `json:"failed"`
	Recent  []FetchEntry `json:"recent"`
}

type fetchRecord struct {
	mu                     sync.Mutex
	tunnel, direct, failed uint64
	recent                 []FetchEntry
}

var fetchLog fetchRecord

func (r *fetchRecord) add(host, via, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch via {
	case "tunnel":
		r.tunnel++
	case "direct":
		r.direct++
	default:
		r.failed++
	}
	// The same site again only moves to the top.
	for i, e := range r.recent {
		if e.Host == host && e.Via == via {
			r.recent = append(r.recent[:i], r.recent[i+1:]...)
			break
		}
	}
	r.recent = append([]FetchEntry{{Host: host, Via: via, Err: msg, At: time.Now().UnixMilli()}}, r.recent...)
	if len(r.recent) > 20 {
		r.recent = r.recent[:20]
	}
}

// FetchStatus reports the downloads listener's counters and recent sites.
func FetchStatus() FetchStats {
	fetchLog.mu.Lock()
	defer fetchLog.mu.Unlock()
	return FetchStats{Running: fetchNow.Load() != nil, Tunnel: fetchLog.tunnel, Direct: fetchLog.direct, Failed: fetchLog.failed,
		Recent: append([]FetchEntry{}, fetchLog.recent...)}
}
