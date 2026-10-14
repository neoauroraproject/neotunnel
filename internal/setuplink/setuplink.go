// Package setuplink encodes the signed neotunnel:// links a master hands to
// its peers. One link both pairs a peer and carries its full configuration,
// so applying it again later is how an unreachable peer catches up.
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

const Scheme = "neotunnel://"

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
	return Scheme + enc.EncodeToString(z.Bytes()) + "." + enc.EncodeToString(sig), nil
}

// Decode verifies the signature against the key embedded in the link. The
// caller decides whether that key is one it trusts.
func Decode(link string) (*Payload, error) {
	link = strings.TrimSpace(link)
	body, ok := strings.CutPrefix(link, Scheme)
	if !ok {
		return nil, fmt.Errorf("not a %s link", Scheme)
	}
	zb64, sb64, ok := strings.Cut(body, ".")
	if !ok {
		return nil, fmt.Errorf("malformed link")
	}
	enc := base64.RawURLEncoding
	z, err1 := enc.DecodeString(zb64)
	sig, err2 := enc.DecodeString(sb64)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("malformed link (truncated when copying?)")
	}
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("malformed link: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("malformed link: %w", err)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("malformed link: %w", err)
	}
	if p.V != 1 {
		return nil, fmt.Errorf("link version %d not supported; upgrade neotunnel", p.V)
	}
	key, err := identity.DecodeKey(p.Master.PublicKey)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, z, sig) {
		return nil, fmt.Errorf("link signature is invalid")
	}
	return &p, nil
}

// Accept merges a verified payload into the peer's state. It refuses links
// from a different master (unless force) and links older than what's applied.
func Accept(st *config.State, p *Payload, force bool) error {
	if m := st.Managed; m != nil && !force {
		if m.Master.PublicKey != p.Master.PublicKey {
			return fmt.Errorf("this node is managed by master %q (%s); link is from %q (%s). Use --force to switch masters",
				m.Master.Name, identity.Fingerprint(m.Master.PublicKey), p.Master.Name, identity.Fingerprint(p.Master.PublicKey))
		}
		if p.Revision < m.Revision {
			return fmt.Errorf("link is older (revision %d) than the applied configuration (revision %d)", p.Revision, m.Revision)
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
