// Package setuplink encodes the signed setup keys a master hands to its
// peers ("NT1.<gzip json>.<ed25519 signature>"). One key both pairs a peer and
// carries its full configuration, so applying it again later is how an
// unreachable peer catches up.
package setuplink

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
)

// Prefix marks a setup key and its format version.
const Prefix = "NT1."

type Payload struct {
	V           int              `json:"v"`
	Issued      time.Time        `json:"issued"`
	Master      config.MasterRef `json:"master"`
	Peer        string           `json:"peer"` // name the master assigned
	EnrollToken string           `json:"enroll,omitempty"`
	Revision    uint64           `json:"rev"`
	Tunnels     []config.Tunnel  `json:"tunnels,omitempty"`
	Forwards    []config.Forward `json:"forwards,omitempty"`
}

// Encode signs p with the master identity.
func Encode(id *identity.Identity, p Payload) (string, error) {
	p.V = 1
	p.Master.PublicKey = id.PublicString()
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var z bytes.Buffer
	w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
	w.Write(raw)
	w.Close()
	sig := ed25519.Sign(id.Key, z.Bytes())
	enc := base64.RawURLEncoding
	return Prefix + enc.EncodeToString(z.Bytes()) + "." + enc.EncodeToString(sig), nil
}

// Decode verifies the signature against the master key embedded in the
// setup key. The caller decides whether that master is one it trusts.
func Decode(setupKey string) (*Payload, error) {
	setupKey = strings.Trim(strings.TrimSpace(setupKey), `'"`)
	body, ok := strings.CutPrefix(setupKey, Prefix)
	if !ok {
		return nil, fmt.Errorf("not a setup key (it starts with %s)", Prefix)
	}
	zb64, sb64, ok := strings.Cut(body, ".")
	if !ok {
		return nil, fmt.Errorf("malformed setup key")
	}
	enc := base64.RawURLEncoding
	z, err1 := enc.DecodeString(zb64)
	sig, err2 := enc.DecodeString(sb64)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("malformed setup key (cut off when copying?)")
	}
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("malformed setup key: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("malformed setup key: %w", err)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("malformed setup key: %w", err)
	}
	if p.V != 1 {
		return nil, fmt.Errorf("setup key version %d not supported; upgrade neotunnel", p.V)
	}
	key, err := identity.DecodeKey(p.Master.PublicKey)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, z, sig) {
		return nil, fmt.Errorf("setup key signature is invalid")
	}
	return &p, nil
}

// Accept merges a verified payload into the peer's state. It refuses keys
// from a different master (unless force) and keys older than what's applied.
func Accept(st *config.State, p *Payload, force bool) error {
	if m := st.Managed; m != nil && !force {
		if m.Master.PublicKey != p.Master.PublicKey {
			return fmt.Errorf("this server is managed by %q (%s); the setup key is from %q (%s). Use --force to switch",
				m.Master.Name, identity.Fingerprint(m.Master.PublicKey), p.Master.Name, identity.Fingerprint(p.Master.PublicKey))
		}
		if p.Revision < m.Revision {
			return fmt.Errorf("setup key is older (revision %d) than the applied configuration (revision %d)", p.Revision, m.Revision)
		}
	}
	token := p.EnrollToken
	if m := st.Managed; token == "" && m != nil && m.Master.PublicKey == p.Master.PublicKey {
		token = m.EnrollToken
	}
	st.Managed = &config.Managed{
		Master:      p.Master,
		Self:        p.Peer,
		Revision:    p.Revision,
		Tunnels:     p.Tunnels,
		Forwards:    p.Forwards,
		EnrollToken: token,
	}
	if st.Self == "" {
		st.Self = p.Peer
	}
	return nil
}
