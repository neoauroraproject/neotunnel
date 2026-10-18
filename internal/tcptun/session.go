// Package tcptun is NeoTunnel's TCP transport, for paths where neither UDP
// nor raw IP protocols (41, 47, ...) pass between two servers: IP packets
// from a TUN interface travel length-prefixed over a long-lived TCP
// connection.
//
// It is a last resort. A TCP stream retransmits every lost segment and holds
// back what follows it, so the TCP flows inside (most traffic) see delays
// instead of losses and their congestion control reacts late: under packet
// loss this is much slower than a UDP or kernel tunnel. On a clean path it
// is close to them.
//
// Both ends listen on the tunnel's port and either may dial: side 1 dials at
// once, side 0 only when no connection has come in for a few seconds. So one
// connection usually carries the tunnel, and a path that lets TCP through in
// one direction only still works. Every connection starts with a mutual
// challenge-response that proves both ends hold the tunnel secret; with a
// cipher chosen, every frame is then sealed with per-connection keys derived
// from the tunnel's HKDF keys and both ends' fresh nonces.
//
// Handshake (dialer D, listener L):
//
//	D→L  version(1) side_D(1) nonceD(32)
//	L→D  nonceL(32) HMAC(auth, "L" side_L nonceD nonceL)
//	D→L  HMAC(auth, "D" side_D nonceD nonceL)
//
// Frame: length(2) | packet (sealed when encrypted). Length 0 is a keepalive.
//
// GSO: an end whose interface hands out super-packets (up to 64 KiB, with
// the kernel's virtio-net header in front) says so in its first frame, a
// control frame no IP packet looks like ("\x00NTG"); an old NeoTunnel
// writes it to its interface, which drops it. An end that gets it and has
// super-packets to send answers "\x00NTS", and every frame it sends after
// that has a 4-byte length and carries header and packet. So each
// direction switches on its own, and only when both ends are new.
package tcptun

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"neotunnel/internal/config"
)

const (
	version   = 1
	nonceSize = 32
	macSize   = sha256.Size
	tagSize   = 16
	maxFrame  = 65535
	// maxBig is the largest frame after the switch: a virtio-net header,
	// a 64 KiB super-packet and a tag.
	maxBig = vnetLen + 65535 + tagSize

	handshakeTimeout = 10 * time.Second
)

var errAuth = errors.New("the far end does not hold this tunnel's secret")

// keys are a tunnel end's long-term keys, all derived from the tunnel secret.
type keys struct {
	auth       []byte
	send, recv []byte // per direction, nil without a cipher
	cipher     string
	side       int
}

func keyLen(c string) (int, error) {
	switch c {
	case "aes-gcm-128":
		return 16, nil
	case "aes-gcm-256", "chacha20-poly1305":
		return 32, nil
	}
	return 0, fmt.Errorf("unknown cipher %q (aes-gcm-128, aes-gcm-256, chacha20-poly1305)", c)
}

func newAEAD(c string, key []byte) (cipher.AEAD, error) {
	if c == "chacha20-poly1305" {
		return chacha20poly1305.New(key)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func deriveKeys(c Config) (*keys, error) {
	return deriveKeysFor("tcp", c.Secret, c.Tunnel, c.Cipher, c.Side)
}

// deriveKeysFor derives keys whose labels start with purpose, so another
// user of the handshake (the relay) never shares keys with a tunnel.
func deriveKeysFor(purpose, secret, tunnel, cph string, side int) (*keys, error) {
	if side != 0 && side != 1 {
		return nil, fmt.Errorf("bad side %d", side)
	}
	auth, err := config.DeriveKey(secret, tunnel, purpose+" auth", 32)
	if err != nil {
		return nil, err
	}
	k := &keys{auth: auth, cipher: cph, side: side}
	if cph == "" {
		return k, nil
	}
	if _, err := keyLen(cph); err != nil {
		return nil, err
	}
	if k.send, err = config.DeriveKey(secret, tunnel, fmt.Sprintf("%s %s %d>%d", purpose, cph, side, 1-side), 32); err != nil {
		return nil, err
	}
	k.recv, err = config.DeriveKey(secret, tunnel, fmt.Sprintf("%s %s %d>%d", purpose, cph, 1-side, side), 32)
	return k, err
}

func mac(key []byte, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// session is one connection's framing state after the handshake.
type session struct {
	seal, open   cipher.AEAD // nil without a cipher
	sendN, recvN uint64
	hdr          [4]byte
	// txBig and rxBig: frames in that direction have a 4-byte length (GSO,
	// see the package comment). rxBig belongs to the reader.
	txBig   atomic.Bool
	rxBig   bool
	nonce   [12]byte
	scratch []byte
}

// session derives the connection's keys: both ends hash their direction's
// tunnel key with the two nonces, so every connection has fresh keys and
// counter nonces never repeat under one key.
func (k *keys) session(nd, nl []byte) (*session, error) {
	s := &session{}
	if k.cipher == "" {
		return s, nil
	}
	n, _ := keyLen(k.cipher)
	var err error
	if s.seal, err = newAEAD(k.cipher, mac(k.send, nd, nl)[:n]); err != nil {
		return nil, err
	}
	if s.open, err = newAEAD(k.cipher, mac(k.recv, nd, nl)[:n]); err != nil {
		return nil, err
	}
	s.scratch = make([]byte, maxBig)
	return s, nil
}

func dialHandshake(c net.Conn, k *keys) (*session, error) {
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	defer c.SetDeadline(time.Time{})
	hello := make([]byte, 2+nonceSize)
	hello[0], hello[1] = version, byte(k.side)
	rand.Read(hello[2:])
	if _, err := c.Write(hello); err != nil {
		return nil, err
	}
	resp := make([]byte, nonceSize+macSize)
	if _, err := io.ReadFull(c, resp); err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	nd, nl := hello[2:], resp[:nonceSize]
	if !hmac.Equal(resp[nonceSize:], mac(k.auth, []byte{'L', byte(1 - k.side)}, nd, nl)) {
		return nil, errAuth
	}
	if _, err := c.Write(mac(k.auth, []byte{'D', byte(k.side)}, nd, nl)); err != nil {
		return nil, err
	}
	return k.session(nd, nl)
}

func acceptHandshake(c net.Conn, k *keys) (*session, error) {
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	defer c.SetDeadline(time.Time{})
	hello := make([]byte, 2+nonceSize)
	if _, err := io.ReadFull(c, hello); err != nil {
		return nil, err
	}
	if hello[0] != version {
		return nil, fmt.Errorf("unknown version %d (update NeoTunnel on both ends)", hello[0])
	}
	if int(hello[1]) != 1-k.side {
		return nil, errAuth // ourselves, or the same side of another link
	}
	nd := hello[2:]
	resp := make([]byte, nonceSize, nonceSize+macSize)
	rand.Read(resp)
	nl := resp[:nonceSize]
	resp = append(resp, mac(k.auth, []byte{'L', byte(k.side)}, nd, nl)...)
	if _, err := c.Write(resp); err != nil {
		return nil, err
	}
	proof := make([]byte, macSize)
	if _, err := io.ReadFull(c, proof); err != nil {
		return nil, err
	}
	if !hmac.Equal(proof, mac(k.auth, []byte{'D', byte(1 - k.side)}, nd, nl)) {
		return nil, errAuth
	}
	return k.session(nd, nl)
}

// maxPacket is the largest packet one frame carries.
func (s *session) maxPacket() int {
	n := maxFrame
	if s.txBig.Load() {
		n = maxBig
	}
	if s.seal != nil {
		n -= tagSize
	}
	return n
}

func (s *session) nextNonce(n *uint64) []byte {
	binary.BigEndian.PutUint64(s.nonce[4:], *n)
	*n++
	return s.nonce[:]
}

// writeFrame buffers one packet (nil: a keepalive). Not safe for concurrent
// use.
func (s *session) writeFrame(w *bufio.Writer, pkt []byte) error {
	if len(pkt) > 0 && s.seal != nil {
		pkt = s.seal.Seal(s.scratch[:0], s.nextNonce(&s.sendN), pkt, nil)
	}
	hdr := s.hdr[:2]
	if s.txBig.Load() {
		hdr = s.hdr[:4]
		binary.BigEndian.PutUint32(hdr, uint32(len(pkt)))
	} else {
		binary.BigEndian.PutUint16(hdr, uint16(len(pkt)))
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(pkt)
	return err
}

// readFrame reads the next frame into buf (len maxFrame, maxBig once rxBig)
// and returns the packet it carries, empty for a keepalive.
func (s *session) readFrame(r *bufio.Reader, buf []byte) ([]byte, error) {
	var hdr [4]byte
	var n int
	if s.rxBig {
		if _, err := io.ReadFull(r, hdr[:4]); err != nil {
			return nil, err
		}
		n = int(binary.BigEndian.Uint32(hdr[:4]))
	} else {
		if _, err := io.ReadFull(r, hdr[:2]); err != nil {
			return nil, err
		}
		n = int(binary.BigEndian.Uint16(hdr[:2]))
	}
	if n > len(buf) {
		return nil, fmt.Errorf("a frame of %d bytes is too large", n)
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return nil, err
	}
	if n == 0 || s.open == nil {
		return buf[:n], nil
	}
	// open has its own nonce buffer: readFrame and writeFrame run on
	// different goroutines.
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], s.recvN)
	s.recvN++
	pkt, err := s.open.Open(buf[:0], nonce[:], buf[:n], nil)
	if err != nil {
		return nil, errors.New("a frame failed authentication")
	}
	return pkt, nil
}
