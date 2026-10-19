package web

// The panel's certificate: self-signed by default (opened by IP), or for a
// domain, either from Let's Encrypt (HTTP-01 on port 80, renewed by the
// daemon before it runs out) or pasted in the panel. The server picks the
// current one on every handshake, so a new certificate needs no restart.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/acme"

	"neotunnel/internal/config"
	"neotunnel/internal/relay"
)

// LetsEncrypt is the ACME directory certificates come from (a variable so a
// test can point it at a staging server).
var LetsEncrypt = acme.LetsEncryptURL

// renewBefore is how long before it runs out a Let's Encrypt certificate is
// replaced.
const renewBefore = 30 * 24 * time.Hour

func domainCertPaths() (crt, key string) {
	return filepath.Join(config.Dir, "panel-domain.crt"), filepath.Join(config.Dir, "panel-domain.key")
}

func acmeKeyPath() string { return filepath.Join(config.Dir, "acme-account.key") }

// panelCert is the domain certificate the panel serves (nil: self-signed).
var panelCert atomic.Pointer[tls.Certificate]

// loadPanelCert puts the configured domain certificate in use, or none.
func loadPanelCert() error {
	st, err := config.Load()
	if err != nil {
		return err
	}
	if st.Panel == nil || st.Panel.Cert == "" {
		panelCert.Store(nil)
		return nil
	}
	c, err := tls.LoadX509KeyPair(domainCertPaths())
	if err != nil {
		panelCert.Store(nil)
		return err
	}
	panelCert.Store(&c)
	return nil
}

// CertView is the panel's domain and certificate as the settings page shows
// them.
type CertView struct {
	Domain   string   `json:"domain,omitempty"`
	Mode     string   `json:"mode"` // acme, custom or self (self-signed)
	Issuer   string   `json:"issuer,omitempty"`
	Names    []string `json:"names,omitempty"`
	Expires  int64    `json:"expires,omitempty"` // unix ms
	Error    string   `json:"error,omitempty"`   // the certificate files can't be read
	Address  string   `json:"address,omitempty"` // this server's public IP
	Resolves []string `json:"resolves,omitempty"`
	Here     bool     `json:"here"`    // the domain points at Address
	Port80   bool     `json:"port80"`  // port 80 is free for Let's Encrypt's check
	Checked  bool     `json:"checked"` // Resolves and Here were looked up
}

func (l *Live) PanelCert(domain string) (CertView, error) {
	st, err := config.Load()
	if err != nil {
		return CertView{}, err
	}
	if st.Panel == nil {
		return CertView{}, errors.New("panel is not set up")
	}
	v := CertView{Domain: st.Panel.Domain, Mode: st.Panel.Cert, Address: st.Address, Port80: port80Free()}
	if v.Mode == "" {
		v.Mode = "self"
	} else if c, err := tls.LoadX509KeyPair(domainCertPaths()); err != nil {
		v.Error = err.Error()
	} else if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
		v.Issuer, v.Names, v.Expires = issuerName(leaf), leaf.DNSNames, leaf.NotAfter.UnixMilli()
	}
	if domain = config.CleanDomain(domain); domain == "" {
		domain = v.Domain
	}
	if domain != "" {
		v.Checked = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if as, err := net.DefaultResolver.LookupHost(ctx, domain); err == nil {
			v.Resolves = as
			v.Here = slices.Contains(as, st.Address)
		}
	}
	return v, nil
}

func issuerName(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0] + " " + c.Issuer.CommonName
	}
	return c.Issuer.CommonName
}

func port80Free() bool {
	l, err := net.Listen("tcp", ":80")
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// IssueCert gets a Let's Encrypt certificate for domain, answering its check
// on port 80 for the minute it takes, and puts it in use.
func (l *Live) IssueCert(ctx context.Context, domain string) (CertView, error) {
	domain = config.CleanDomain(domain)
	if err := config.ValidDomain(domain); err != nil {
		return CertView{}, err
	}
	if err := issue(ctx, domain); err != nil {
		return CertView{}, err
	}
	if err := setPanelDomain(domain, config.CertACME); err != nil {
		return CertView{}, err
	}
	return l.PanelCert("")
}

// SetCustomCert puts a pasted certificate and key in use for domain (or the
// certificate's first name when domain is empty).
func (l *Live) SetCustomCert(domain, certPEM, keyPEM string) (CertView, error) {
	c, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return CertView{}, fmt.Errorf("the certificate and key don't fit together: %w", err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return CertView{}, err
	}
	if time.Now().After(leaf.NotAfter) {
		return CertView{}, fmt.Errorf("the certificate ran out on %s", leaf.NotAfter.Format("2006-01-02"))
	}
	domain = config.CleanDomain(domain)
	if domain == "" && len(leaf.DNSNames) > 0 {
		domain = strings.TrimPrefix(leaf.DNSNames[0], "*.")
	}
	if domain == "" {
		return CertView{}, errors.New("enter the panel's domain")
	}
	if err := config.ValidDomain(domain); err != nil {
		return CertView{}, err
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return CertView{}, fmt.Errorf("the certificate isn't for %s (it names %s)", domain, strings.Join(leaf.DNSNames, ", "))
	}
	if err := writeCert([]byte(certPEM), []byte(keyPEM)); err != nil {
		return CertView{}, err
	}
	if err := setPanelDomain(domain, config.CertCustom); err != nil {
		return CertView{}, err
	}
	return l.PanelCert("")
}

// RemoveCert goes back to the self-signed certificate, opened by IP.
func (l *Live) RemoveCert() error { return setPanelDomain("", "") }

func setPanelDomain(domain, mode string) error {
	_, err := config.Update(func(s *config.State) error {
		if s.Panel == nil {
			return errors.New("panel is not set up")
		}
		s.Panel.Domain, s.Panel.Cert = domain, mode
		return nil
	})
	if err != nil {
		return err
	}
	return loadPanelCert()
}

func writeCert(certPEM, keyPEM []byte) error {
	crt, key := domainCertPaths()
	if err := writeFile(key, keyPEM, 0o600); err != nil {
		return err
	}
	return writeFile(crt, certPEM, 0o644)
}

// writeFile replaces a file atomically.
func writeFile(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// issue runs the ACME order for domain and saves the certificate.
func issue(ctx context.Context, domain string) error {
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		return errors.New("port 80 is taken by another program (nginx, Xray, ...): Let's Encrypt checks the domain on port 80; stop that program for a minute, or paste a certificate of your own")
	}
	tokens := map[string]string{}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := tokens[r.URL.Path]; ok {
			w.Write([]byte(v))
			return
		}
		http.NotFound(w, r)
	})}
	defer srv.Close()

	key, err := accountKey()
	if err != nil {
		return err
	}
	// Let's Encrypt itself may be filtered here: its requests take the
	// downloads tunnel when it is on and lists it.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) { return relay.FetchDial(ctx, addr, "") }
	cl := &acme.Client{Key: key, DirectoryURL: LetsEncrypt, HTTPClient: &http.Client{Transport: tr, Timeout: time.Minute}}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := cl.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return fmt.Errorf("Let's Encrypt: %w", err)
	}
	order, err := cl.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return fmt.Errorf("Let's Encrypt: %w", err)
	}
	// Every token is in place before the server starts answering.
	var accept []*acme.Challenge
	for _, u := range order.AuthzURLs {
		z, err := cl.GetAuthorization(ctx, u)
		if err != nil {
			return fmt.Errorf("Let's Encrypt: %w", err)
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var ch *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "http-01" {
				ch = c
			}
		}
		if ch == nil {
			return errors.New("Let's Encrypt offered no HTTP check for this domain")
		}
		resp, err := cl.HTTP01ChallengeResponse(ch.Token)
		if err != nil {
			return err
		}
		tokens[cl.HTTP01ChallengePath(ch.Token)] = resp
		accept = append(accept, ch)
	}
	go srv.Serve(ln)
	for _, ch := range accept {
		if _, err := cl.Accept(ctx, ch); err != nil {
			return fmt.Errorf("Let's Encrypt: %w", err)
		}
	}
	for _, u := range order.AuthzURLs {
		if _, err := cl.WaitAuthorization(ctx, u); err != nil {
			return fmt.Errorf("Let's Encrypt couldn't reach http://%s/ on this server (does the domain point here, and is port 80 open in the provider's and this server's firewall, e.g. sudo ufw allow 80/tcp?): %w", domain, err)
		}
	}
	if order, err = cl.WaitOrder(ctx, order.URI); err != nil {
		return fmt.Errorf("Let's Encrypt: %w", err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}}, priv)
	if err != nil {
		return err
	}
	ders, _, err := cl.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("Let's Encrypt: %w", err)
	}
	var chain []byte
	for _, d := range ders {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	kb, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	return writeCert(chain, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

// accountKey is this server's Let's Encrypt account key, made on first use.
func accountKey() (crypto.Signer, error) {
	if b, err := os.ReadFile(acmeKeyPath()); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
				return k, nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return k, writeFile(acmeKeyPath(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
}

// renewCerts replaces the Let's Encrypt certificate a month before it runs
// out, checking twice a day.
func renewCerts(ctx context.Context) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		if st, err := config.Load(); err == nil && st.Panel != nil && st.Panel.Cert == config.CertACME {
			if c := panelCert.Load(); c == nil || len(c.Certificate) == 0 || expiresSoon(c.Certificate[0]) {
				if err := issue(ctx, st.Panel.Domain); err != nil {
					log.Printf("panel certificate for %s: renewal failed: %v", st.Panel.Domain, err)
				} else {
					log.Printf("panel certificate for %s renewed", st.Panel.Domain)
					loadPanelCert()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func expiresSoon(der []byte) bool {
	c, err := x509.ParseCertificate(der)
	return err != nil || time.Until(c.NotAfter) < renewBefore
}
