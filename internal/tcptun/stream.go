package tcptun

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
)

// StreamKeys let another part of NeoTunnel (the relay) run this package's
// handshake on its own connections and, with a cipher, carry a byte stream
// in sealed frames. Keys are derived under their own purpose label, so they
// never match a TCP tunnel's.
type StreamKeys struct{ k *keys }

// NewStreamKeys derives side's keys for purpose from a tunnel's secret.
// cipher "" authenticates the connection and leaves the bytes plain.
func NewStreamKeys(purpose, secret, tunnel, cipher string, side int) (*StreamKeys, error) {
	k, err := deriveKeysFor(purpose, secret, tunnel, cipher, side)
	if err != nil {
		return nil, err
	}
	return &StreamKeys{k: k}, nil
}

// ValidCipher checks a stream cipher name ("" is none).
func ValidCipher(c string) error {
	if c == "" {
		return nil
	}
	_, err := keyLen(c)
	return err
}

// ErrAuth: the far end does not hold the secret.
var ErrAuth = errAuth

// Dial runs the dialer's handshake on c. Without a cipher it returns c
// itself, so copies to and from it stay zero-copy (splice); with one, a
// Stream.
func (s *StreamKeys) Dial(c net.Conn) (net.Conn, error) {
	ss, err := dialHandshake(c, s.k)
	if err != nil {
		return nil, err
	}
	return s.wrap(c, ss), nil
}

// Accept runs the listener's handshake on c (see Dial).
func (s *StreamKeys) Accept(c net.Conn) (net.Conn, error) {
	ss, err := acceptHandshake(c, s.k)
	if err != nil {
		return nil, err
	}
	return s.wrap(c, ss), nil
}

func (s *StreamKeys) wrap(c net.Conn, ss *session) net.Conn {
	if ss.seal == nil {
		return c
	}
	return &Stream{Conn: c, s: ss, r: bufio.NewReaderSize(c, 64<<10), w: bufio.NewWriterSize(c, 64<<10), buf: make([]byte, maxFrame)}
}

// Stream is an encrypted byte stream over a connection: data goes in sealed
// frames, and an empty frame marks the end of the sender's data, so a
// connection cut short (or an injected FIN) is an error, not a clean end.
type Stream struct {
	net.Conn
	s *session

	r       *bufio.Reader
	buf     []byte
	pending []byte
	eof     bool

	wmu    sync.Mutex
	w      *bufio.Writer
	closed bool // CloseWrite done
}

func (t *Stream) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		if t.eof {
			return 0, io.EOF
		}
		pkt, err := t.s.readFrame(t.r, t.buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if len(pkt) == 0 {
			t.eof = true
			continue
		}
		t.pending = pkt
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *Stream) Write(p []byte) (int, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if t.closed {
		return 0, net.ErrClosed
	}
	max := t.s.maxPacket()
	n := 0
	for n < len(p) {
		chunk := p[n:min(len(p), n+max)]
		if err := t.s.writeFrame(t.w, chunk); err != nil {
			return n, err
		}
		n += len(chunk)
	}
	return n, t.w.Flush()
}

// CloseWrite sends the end-of-data frame and half-closes the connection.
func (t *Stream) CloseWrite() error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if err := t.s.writeFrame(t.w, nil); err != nil {
		return err
	}
	if err := t.w.Flush(); err != nil {
		return err
	}
	if cw, ok := t.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
