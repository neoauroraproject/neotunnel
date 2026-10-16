// Package obfs is NeoObfs, NeoTunnel's user-space tunnel for paths where the
// kernel tunnels are filtered or recognized: IP packets travel inside UDP
// datagrams that are encrypted end to end (XChaCha20-Poly1305) and carry no
// fixed bytes at all, so to an observer every datagram, its first bytes
// included, is random noise of varying size. Kernel tunnels can be stacked
// on top of it like on any other carrier.
//
// Datagram: nonce(24) | seal( type(1) padlen(1) seq(8) time(4) | packet | padding )
//
// seq starts at the sender's clock in nanoseconds and counts up, so it keeps
// growing across restarts; the receiver rejects replays with a sliding
// window. time (unix seconds) lets a freshly started receiver refuse
// recorded old traffic.
package obfs

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	nonceLen  = chacha20poly1305.NonceSizeX
	tagLen    = chacha20poly1305.Overhead
	headerLen = 14
	// Overhead is the size a datagram adds to the packet it carries.
	Overhead = nonceLen + tagLen + headerLen

	typeData      = 1
	typeKeepalive = 2

	windowSize = 4096 // replay window, in packets
	// maxSkew bounds the clock difference a receiver accepts when it starts
	// tracking a sender.
	maxSkew = 15 * time.Minute
)

var errAuth = errors.New("not a datagram for this tunnel")

// codec seals outgoing and opens incoming datagrams of one tunnel end.
type codec struct {
	send, recv cipher.AEAD
	padding    int
	seq        uint64
	win        window
	now        func() time.Time
}

func newCodec(sendKey, recvKey []byte, padding int) (*codec, error) {
	s, err := chacha20poly1305.NewX(sendKey)
	if err != nil {
		return nil, err
	}
	r, err := chacha20poly1305.NewX(recvKey)
	if err != nil {
		return nil, err
	}
	if padding > 255 {
		padding = 255
	}
	return &codec{send: s, recv: r, padding: padding, seq: uint64(time.Now().UnixNano()), now: time.Now}, nil
}

// room is the buffer size seal needs for a packet of n bytes.
func (c *codec) room(n int) int { return n + Overhead + c.padding }

// seal writes the datagram for pkt into dst (len(dst) >= room(len(pkt)))
// and returns it. Safe for concurrent use.
func (c *codec) seal(dst []byte, typ byte, pkt []byte, ts uint32) []byte {
	pad := 0
	if c.padding > 0 {
		var b [1]byte
		rand.Read(b[:])
		pad = int(b[0]) % (c.padding + 1)
	}
	nonce := dst[:nonceLen]
	rand.Read(nonce)
	plain := dst[nonceLen : nonceLen+headerLen+len(pkt)+pad]
	plain[0], plain[1] = typ, byte(pad)
	binary.BigEndian.PutUint64(plain[2:], atomic.AddUint64(&c.seq, 1))
	binary.BigEndian.PutUint32(plain[10:], ts)
	copy(plain[headerLen:], pkt)
	clear(plain[headerLen+len(pkt):])
	out := c.send.Seal(plain[:0], nonce, plain, nil)
	return dst[:nonceLen+len(out)]
}

// open authenticates and decrypts a datagram in place, returning its type
// and the packet it carries. Not safe for concurrent use.
func (c *codec) open(d []byte) (byte, []byte, error) {
	if len(d) < Overhead {
		return 0, nil, errAuth
	}
	plain, err := c.recv.Open(d[nonceLen:nonceLen], d[:nonceLen], d[nonceLen:], nil)
	if err != nil {
		return 0, nil, errAuth
	}
	typ, pad := plain[0], int(plain[1])
	seq := binary.BigEndian.Uint64(plain[2:])
	ts := int64(binary.BigEndian.Uint32(plain[10:]))
	if len(plain) < headerLen+pad {
		return 0, nil, errAuth
	}
	if c.win.top == 0 {
		if d := time.Duration(c.now().Unix()-ts) * time.Second; d > maxSkew || d < -maxSkew {
			return 0, nil, errAuth
		}
	}
	if !c.win.accept(seq) {
		return 0, nil, errAuth
	}
	return typ, plain[headerLen : len(plain)-pad], nil
}

// window is an anti-replay sliding window over sequence numbers.
type window struct {
	top  uint64 // highest accepted
	bits [windowSize / 64]uint64
}

func (w *window) accept(seq uint64) bool {
	switch {
	case w.top == 0 || seq > w.top:
		if w.top == 0 || seq-w.top >= windowSize {
			w.bits = [windowSize / 64]uint64{}
		} else {
			for s := w.top + 1; s < seq; s++ {
				w.clear(s)
			}
		}
		w.top = seq
	case w.top-seq >= windowSize, w.has(seq):
		return false
	}
	w.set(seq)
	return true
}

func (w *window) idx(s uint64) (int, uint64) {
	i := s % windowSize
	return int(i / 64), 1 << (i % 64)
}
func (w *window) set(s uint64)      { i, m := w.idx(s); w.bits[i] |= m }
func (w *window) clear(s uint64)    { i, m := w.idx(s); w.bits[i] &^= m }
func (w *window) has(s uint64) bool { i, m := w.idx(s); return w.bits[i]&m != 0 }
