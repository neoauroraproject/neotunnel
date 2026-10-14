// Package identity gives every node an Ed25519 key. The key signs setup
// links and backs a self-signed TLS certificate; peers pin each other's keys,
// so no CA or domain is ever needed.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"neotunnel/internal/config"
)

type Identity struct {
	Key ed25519.PrivateKey
}

func path() string { return filepath.Join(config.Dir, "identity.key") }

// LoadOrCreate reads the node key, generating one on first run.
func LoadOrCreate() (*Identity, error) {
	b, err := os.ReadFile(path())
	if errors.Is(err, fs.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(config.Dir, 0o700); err != nil {
			return nil, err
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if err := os.WriteFile(path(), pemBytes, 0o600); err != nil {
			return nil, err
		}
		return &Identity{Key: priv}, nil
	}
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: not a PEM key", path())
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an ed25519 key", path())
	}
	return &Identity{Key: priv}, nil
}

func (id *Identity) Public() ed25519.PublicKey { return id.Key.Public().(ed25519.PublicKey) }

// PublicString is the base64 form stored in state and links.
func (id *Identity) PublicString() string { return EncodeKey(id.Public()) }

func EncodeKey(k ed25519.PublicKey) string { return base64.RawURLEncoding.EncodeToString(k) }

func DecodeKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key")
	}
	return ed25519.PublicKey(b), nil
}

// Fingerprint is a short human-checkable form of a key.
func Fingerprint(k string) string {
	if len(k) > 12 {
		return k[:6] + "…" + k[len(k)-6:]
	}
	return k
}

// Certificate builds a throwaway self-signed TLS cert for this key.
func (id *Identity) Certificate() (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "neotunnel"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, id.Public(), id.Key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.Key}, nil
}

// PeerKey extracts the Ed25519 key from a peer's leaf certificate.
func PeerKey(certs []*x509.Certificate) (string, error) {
	if len(certs) == 0 {
		return "", fmt.Errorf("no certificate presented")
	}
	k, ok := certs[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("certificate is not ed25519")
	}
	return EncodeKey(k), nil
}

// PinnedClient returns a TLS config that only accepts a server holding want.
func (id *Identity) PinnedClient(want string) (*tls.Config, error) {
	cert, err := id.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // replaced by key pinning below
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return fmt.Errorf("server sent no certificate")
			}
			c, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			got, err := PeerKey([]*x509.Certificate{c})
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("server key %s does not match pinned %s", Fingerprint(got), Fingerprint(want))
			}
			return nil
		},
	}, nil
}

// Server returns a TLS config that requests (but doesn't verify) client
// certs; handlers check the client key against what they trust. Certs are
// optional at the TLS level so the token-gated binary download works with curl.
func (id *Identity) Server() (*tls.Config, error) {
	cert, err := id.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
