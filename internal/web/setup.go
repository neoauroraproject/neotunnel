// Package web is the NeoTunnel panel: an HTTPS JSON API plus an embedded
// single-page UI, served by the daemon on its own port under a secret path.
package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"neotunnel/internal/config"
)

const (
	portMin = 20000
	portMax = 60000
)

// reserved ports the panel must never take.
var reserved = map[int]bool{7710: true, 7711: true}

// PortFree reports whether a TCP port can be listened on right now.
func PortFree(port int) bool {
	if port < 1 || port > 65535 || reserved[port] {
		return false
	}
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// RandomPort picks a free port in [20000, 60000).
func RandomPort() (int, error) {
	for i := 0; i < 200; i++ {
		p := portMin + mrand.IntN(portMax-portMin)
		if PortFree(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port found")
}

const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func HashPassword(p string) (string, error) {
	if len(p) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

func checkPassword(hash, p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}

// Credentials are shown once, when the panel is set up or reset.
type Credentials struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
}

// Setup creates or updates the panel configuration. port 0 keeps the current
// port or picks a random free one; empty password generates one.
func Setup(port int, user, password string) (Credentials, error) {
	if port != 0 && !PortFree(port) {
		return Credentials{}, fmt.Errorf("port %d is in use or reserved", port)
	}
	if password == "" {
		password = randomString(14)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Credentials{}, err
	}
	st, err := config.Update(func(s *config.State) error {
		p := s.Panel
		if p == nil {
			p = &config.Panel{User: "admin", Path: "/" + randomString(10)}
		}
		switch {
		case port != 0:
			p.Port = port
		case p.Port == 0:
			if p.Port, err = RandomPort(); err != nil {
				return err
			}
		}
		if user != "" {
			p.User = user
		}
		p.PassHash = hash
		s.Panel = p
		return nil
	})
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{URL: PanelURL(st), User: st.Panel.User, Password: password}, nil
}

// PanelURL is the address to open in a browser.
func PanelURL(st *config.State) string {
	if st.Panel == nil {
		return ""
	}
	host := st.Address
	if host == "" {
		host = "SERVER-IP"
	}
	return fmt.Sprintf("https://%s%s/", net.JoinHostPort(host, strconv.Itoa(st.Panel.Port)), st.Panel.Path)
}

// certificate loads (or creates) the panel's self-signed ECDSA certificate.
// ECDSA, not the node's Ed25519 key: browsers don't accept Ed25519 certs.
func certificate(address string) (tls.Certificate, error) {
	crt, key := filepath.Join(config.Dir, "panel.crt"), filepath.Join(config.Dir, "panel.key")
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return c, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "NeoTunnel panel", Organization: []string{"NeoTunnel"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	if a, err := netip.ParseAddr(address); err == nil {
		tmpl.IPAddresses = []net.IP{net.IP(a.AsSlice())}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	os.MkdirAll(config.Dir, 0o700)
	os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return tls.LoadX509KeyPair(crt, key)
}
