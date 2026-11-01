package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/hkdf"

	"neotunnel/internal/identity"
)

// A filtered path can let TCP through and still stall a TLS session to a bare
// IP: it knows a ClientHello by its first bytes, and it may pass the first
// one and none after it. So the control channel doesn't look like TLS on the
// wire. A veil wraps the connection in a keystream from the first byte, keyed
// by the server's public key and a random nonce, which leaves nothing to
// recognize. The veil hides the shape only and adds no security: TLS with both
// keys pinned still runs inside it, and what that carries (tunnel secrets, the
// right to change a server) is why it can't simply be left out.
//
// One port serves both: a first byte of 0x16 is a TLS handshake record, from
// an older node or the token-gated download with curl. A veil's never is.
//
// On the wire, client to server: a 32-byte nonce, then under the keystream one
// byte of padding length, that much padding, and the TLS stream.

const (
	tlsHandshakeRecord = 0x16
	veilNonceSize      = 32
)

// veilStreams derives the two directions' keystreams from a nonce.
func veilStreams(serverKey string, nonce []byte) (toServer, toClient cipher.Stream, err error) {
	pub, err := identity.DecodeKey(serverKey)
	if err != nil {
		return nil, nil, err
	}
	stream := func(label string) (cipher.Stream, error) {
		k := make([]byte, 32+aes.BlockSize)
		if _, err := io.ReadFull(hkdf.New(sha256.New, pub, nonce, []byte("neotunnel/veil/"+label)), k); err != nil {
			return nil, err
		}
		b, err := aes.NewCipher(k[:32])
		if err != nil {
			return nil, err
		}
		return cipher.NewCTR(b, k[32:]), nil
	}
	if toServer, err = stream("to-server"); err != nil {
		return nil, nil, err
	}
	toClient, err = stream("to-client")
	return toServer, toClient, err
}

// veilConn applies a keystream each way. Reads and writes may run at once;
// each uses only its own stream.
type veilConn struct {
	net.Conn
	in, out cipher.Stream
}

func (c *veilConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.in.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (c *veilConn) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	c.out.XORKeyStream(buf, p)
	return c.Conn.Write(buf)
}

// veilClient wraps a new connection to the holder of serverKey. Random padding
// keeps the first packet's size from saying anything either.
func veilClient(conn net.Conn, serverKey string) (net.Conn, error) {
	var nonce [veilNonceSize]byte
	var pad [1]byte
	if _, err := rand.Read(pad[:]); err != nil {
		return nil, err
	}
	for {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		if nonce[0] != tlsHandshakeRecord {
			break
		}
	}
	toServer, toClient, err := veilStreams(serverKey, nonce[:])
	if err != nil {
		return nil, err
	}
	head := make([]byte, 1+int(pad[0]))
	if _, err := rand.Read(head); err != nil {
		return nil, err
	}
	head[0] = pad[0]
	toServer.XORKeyStream(head, head)
	if _, err := conn.Write(append(nonce[:], head...)); err != nil {
		return nil, err
	}
	return &veilConn{Conn: conn, in: toClient, out: toServer}, nil
}

// veilListener accepts both veiled and plain TLS connections on one port.
type veilListener struct {
	net.Listener
	key string
}

func (l veilListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &sniffConn{Conn: c, key: l.key}, nil
}

// sniffConn tells the two kinds apart at its first read, so a client that
// sends nothing holds up neither Accept nor anyone else. What the server's
// handshake deadline covers covers this too.
type sniffConn struct {
	net.Conn
	key   string
	once  sync.Once
	err   error
	first []byte // a plain connection's first byte, not yet read
	veil  *veilConn
}

func (c *sniffConn) sniff() {
	var nonce [veilNonceSize]byte
	if _, c.err = io.ReadFull(c.Conn, nonce[:1]); c.err != nil {
		return
	}
	if nonce[0] == tlsHandshakeRecord {
		c.first = []byte{nonce[0]}
		return
	}
	if _, c.err = io.ReadFull(c.Conn, nonce[1:]); c.err != nil {
		return
	}
	toServer, toClient, err := veilStreams(c.key, nonce[:])
	if err != nil {
		c.err = err
		return
	}
	v := &veilConn{Conn: c.Conn, in: toServer, out: toClient}
	var pad [1]byte
	if _, c.err = io.ReadFull(v, pad[:]); c.err != nil {
		return
	}
	if _, c.err = io.CopyN(io.Discard, v, int64(pad[0])); c.err != nil {
		return
	}
	c.veil = v
}

func (c *sniffConn) Read(p []byte) (int, error) {
	c.once.Do(c.sniff)
	switch {
	case c.err != nil:
		return 0, c.err
	case len(c.first) > 0:
		n := copy(p, c.first)
		c.first = c.first[n:]
		return n, nil
	case c.veil != nil:
		return c.veil.Read(p)
	}
	return c.Conn.Read(p)
}

func (c *sniffConn) Write(p []byte) (int, error) {
	c.once.Do(c.sniff)
	if c.err != nil {
		return 0, c.err
	}
	if c.veil != nil {
		return c.veil.Write(p)
	}
	return c.Conn.Write(p)
}
