package engine

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"

	"neotunnel/internal/config"
)

// WireGuard keys are derived from the tunnel secret, one key pair per side,
// so both ends agree without exchanging anything. A far end that isn't a
// NeoTunnel server (e.g. a MikroTik) brings its own key in PeerKey.

func wgPrivate(secret, tunnel string, side int) ([32]byte, error) {
	var k [32]byte
	b, err := config.DeriveKey(secret, tunnel, fmt.Sprintf("wireguard side %d", side), 32)
	if err != nil {
		return k, err
	}
	copy(k[:], b)
	// Curve25519 clamping.
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

func wgPublic(priv [32]byte) [32]byte {
	var pub [32]byte
	p, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
	copy(pub[:], p)
	return pub
}

// wgKeys returns this side's private key and the far end's public key.
func wgKeys(s LinkSpec) (priv, peer [32]byte, err error) {
	if priv, err = wgPrivate(s.Secret, s.Tunnel, s.Side); err != nil {
		return
	}
	if s.PeerKey != "" {
		b, derr := base64.StdEncoding.DecodeString(s.PeerKey)
		if derr != nil || len(b) != 32 {
			err = fmt.Errorf("peer key is not a WireGuard public key")
			return
		}
		copy(peer[:], b)
		return
	}
	other, err := wgPrivate(s.Secret, s.Tunnel, 1-s.Side)
	if err != nil {
		return
	}
	return priv, wgPublic(other), nil
}

// WireGuardPublicKey returns the public key of a side of a tunnel, to give
// to a far end that is configured by hand.
func WireGuardPublicKey(t config.Tunnel, side int) (string, error) {
	priv, err := wgPrivate(t.Secret, t.Name, side)
	if err != nil {
		return "", err
	}
	pub := wgPublic(priv)
	return base64.StdEncoding.EncodeToString(pub[:]), nil
}
