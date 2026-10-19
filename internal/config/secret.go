package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Cipher is a kernel IPsec (ESP) algorithm a tunnel can be encrypted with.
type Cipher struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Algo is the kernel AEAD name; KeyLen includes the 4-byte salt.
	Algo   string `json:"-"`
	KeyLen int    `json:"-"`
}

// ESPOverhead is the worst-case cost of ESP transport mode with a 16-byte
// tag: SPI 4 + sequence 4 + IV 8 + padding 3 + trailer 2 + tag 16, rounded up.
const ESPOverhead = 38

var ciphers = []Cipher{
	{Name: "aes-gcm-128", Title: "AES-128-GCM", Algo: "rfc4106(gcm(aes))", KeyLen: 20},
	{Name: "aes-gcm-256", Title: "AES-256-GCM", Algo: "rfc4106(gcm(aes))", KeyLen: 36},
	{Name: "chacha20-poly1305", Title: "ChaCha20-Poly1305", Algo: "rfc7539esp(chacha20,poly1305)", KeyLen: 36},
}

func Ciphers() []Cipher { return ciphers }

func LookupCipher(name string) (Cipher, error) {
	for _, c := range ciphers {
		if c.Name == name {
			return c, nil
		}
	}
	return Cipher{}, fmt.Errorf("unknown cipher %q (aes-gcm-128, aes-gcm-256, chacha20-poly1305)", name)
}

// NewSecret returns 32 random bytes, base64.
func NewSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// NeedsSecret reports whether the tunnel derives keys from Secret.
func (t Tunnel) NeedsSecret() bool {
	ki, _ := LookupKind(t.Kind)
	return t.Encrypt != "" || ki.Encrypted || ki.NeedsPeer
}

// DeriveKey expands the tunnel secret into n bytes of key material for
// label. Both ends derive the same keys; labels name the direction.
func (t Tunnel) DeriveKey(label string, n int) ([]byte, error) {
	return DeriveKey(t.Secret, t.KeyName(), label, n)
}

// DeriveKey expands a tunnel's base64 secret (see Tunnel.DeriveKey).
func DeriveKey(secret64, tunnel, label string, n int) ([]byte, error) {
	secret, err := base64.StdEncoding.DecodeString(secret64)
	if err != nil || len(secret) < 16 {
		return nil, fmt.Errorf("tunnel %s has no valid secret", tunnel)
	}
	out := make([]byte, n)
	r := hkdf.New(sha256.New, secret, []byte("neotunnel/"+tunnel), []byte(label))
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
