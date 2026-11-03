package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/xtaci/smux"

	"neotunnel/internal/config"
	"neotunnel/internal/tcptun"
)

// ekmLabel is the TLS exporter label the session keys are bound to.
const ekmLabel = "EXPORTER-neotunnel-session"

func purpose(kind config.Kind, cipher string, ekm []byte) string {
	if kind == config.KindStreamTLS {
		return "session tls " + hex.EncodeToString(ekm)
	}
	if cipher == "" {
		cipher = "none"
	}
	return "session tcp " + cipher
}

func preamble(tunnel, cipher string) []byte {
	b := []byte{'N', 'T', 'S', version, byte(len(tunnel))}
	b = append(b, tunnel...)
	b = append(b, byte(len(cipher)))
	return append(b, cipher...)
}

// dialPort opens one session to the far end on port.
func (g *group) dialPort(port uint16, timeout time.Duration) (*smux.Session, error) {
	s := g.spec
	to := netip.AddrPortFrom(s.Remote, port)
	d := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	c, err := d.Dial("tcp", to.String())
	if err != nil {
		return nil, err
	}
	tcptun.Tune(c.(*net.TCPConn), notSentLowat)
	m := newMeter(c)
	m.attach(g.ctr, g.lim)
	var conn net.Conn = m
	var ekm []byte
	cipher := s.Cipher
	if s.Kind == config.KindStreamTLS {
		cipher = ""
		conf := &utls.Config{ServerName: s.SNI, InsecureSkipVerify: true}
		u := utls.UClient(m, conf, utls.HelloChrome_Auto)
		u.SetDeadline(time.Now().Add(handshakeTimeout))
		if err := u.Handshake(); err != nil {
			c.Close()
			return nil, fmt.Errorf("TLS with %s: %w", to, err)
		}
		u.SetDeadline(time.Time{})
		if u.ConnectionState().Version != utls.VersionTLS13 {
			c.Close()
			return nil, fmt.Errorf("TLS with %s: not TLS 1.3", to)
		}
		// The browser's hello offers renegotiation, which TLS 1.3 doesn't
		// have; with it on, the exporter stays off.
		conf.Renegotiation = utls.RenegotiateNever
		cs := u.ConnectionState()
		if ekm, err = cs.ExportKeyingMaterial(ekmLabel, nil, 32); err != nil {
			c.Close()
			return nil, err
		}
		conn = u
	}
	if _, err := conn.Write(preamble(s.Tunnel, cipher)); err != nil {
		c.Close()
		return nil, err
	}
	k, err := tcptun.NewStreamKeys(purpose(s.Kind, cipher, ekm), s.Secret, s.Tunnel, cipher, s.Side)
	if err != nil {
		c.Close()
		return nil, err
	}
	sc, err := k.Dial(conn)
	if err != nil {
		c.Close()
		if errors.Is(err, tcptun.ErrAuth) {
			return nil, fmt.Errorf("%s does not hold this tunnel's secret: %w", to, err)
		}
		return nil, fmt.Errorf("handshake with %s: %w", to, err)
	}
	ss, err := smux.Client(sc, smuxConfig())
	if err != nil {
		c.Close()
		return nil, err
	}
	return ss, nil
}

// readPreamble reads a session's preamble after "NTS".
func readPreamble(c net.Conn) (tunnel, cipher string, err error) {
	var h [2]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return "", "", err
	}
	if h[0] != version {
		return "", "", fmt.Errorf("unknown session version %d (update NeoTunnel on both ends)", h[0])
	}
	name := make([]byte, int(h[1])+1) // the name, then the cipher's length
	if _, err := io.ReadFull(c, name); err != nil {
		return "", "", err
	}
	ciph := make([]byte, name[len(name)-1])
	if _, err := io.ReadFull(c, ciph); err != nil {
		return "", "", err
	}
	return string(name[:len(name)-1]), string(ciph), nil
}

// ---------- TLS ----------

// serverConfig is the listeners' TLS: a self-signed certificate made at
// start (the client checks nothing in it; the session handshake, bound to
// the TLS keys, proves who is on the other end) and HTTP/1.1, which a
// browser-like client offers and the web server a prober meets speaks.
func (h *Hub) serverConfig() (*tls.Config, error) {
	h.certOnce.Do(func() {
		h.tlsConf, h.certErr = newServerConfig()
	})
	return h.tlsConf, h.certErr
}

func newServerConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    now.Add(-30 * 24 * time.Hour),
		NotAfter:     now.Add(3 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}, nil
}

// ---------- the web server whoever lacks the secret meets ----------

const decoyBody = "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

// Decoy answers a connection that isn't NeoTunnel's like a web server with
// nothing at that address, and closes it.
func Decoy(c net.Conn) {
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "HTTP/1.1 404 Not Found\r\nServer: nginx\r\nDate: %s\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		time.Now().UTC().Format(http1Date), len(decoyBody), decoyBody)
}

const http1Date = "Mon, 02 Jan 2006 15:04:05 GMT"
