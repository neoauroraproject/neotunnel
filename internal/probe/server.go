// Package probe measures links: UDP echo for latency/jitter/loss and TCP
// streams for throughput. The server only answers holders of a session token
// that the master opened through the authenticated agent API, so the port
// can't be abused as a bandwidth sink.
package probe

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"sync"
	"time"
)

const (
	DefaultPort = 7711
	tokenLen    = 16
	maxTestTime = 60 * time.Second
)

type Token [tokenLen]byte

func NewToken() Token {
	var t Token
	rand.Read(t[:])
	return t
}

func (t Token) String() string { return hex.EncodeToString(t[:]) }

func ParseToken(s string) (Token, bool) {
	var t Token
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != tokenLen {
		return t, false
	}
	copy(t[:], b)
	return t, true
}

type Server struct {
	mu       sync.Mutex
	sessions map[Token]time.Time
}

func NewServer() *Server { return &Server{sessions: map[Token]time.Time{}} }

// Open authorizes t for d.
func (s *Server) Open(t Token, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, k)
		}
	}
	s.sessions[t] = now.Add(d)
}

func (s *Server) valid(b []byte) bool {
	if len(b) < tokenLen {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.sessions {
		if subtle.ConstantTimeCompare(k[:], b[:tokenLen]) == 1 && now.Before(exp) {
			return true
		}
	}
	return false
}

// Serve listens on addr (e.g. ":7711") for both UDP and TCP until an error.
func (s *Server) Serve(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pc.Close()
		return err
	}
	go s.serveUDP(pc)
	return s.serveTCP(ln)
}

func (s *Server) serveUDP(pc net.PacketConn) {
	buf := make([]byte, 65536)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if s.valid(buf[:n]) {
			pc.WriteTo(buf[:n], from)
		}
	}
}

func (s *Server) serveTCP(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleTCP(c)
	}
}

// TCP request: token(16) | mode(1: 'U' client sends, 'D' server sends) | duration ms(4)
func (s *Server) handleTCP(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, tokenLen+5)
	if _, err := io.ReadFull(c, hdr); err != nil || !s.valid(hdr) {
		return
	}
	d := time.Duration(binary.BigEndian.Uint32(hdr[tokenLen+1:])) * time.Millisecond
	if d > maxTestTime {
		d = maxTestTime
	}
	switch hdr[tokenLen] {
	case 'U':
		c.SetReadDeadline(time.Now().Add(d + 5*time.Second))
		n, _ := io.Copy(io.Discard, c)
		var out [8]byte
		binary.BigEndian.PutUint64(out[:], uint64(n))
		c.Write(out[:])
	case 'D':
		buf := make([]byte, 128<<10)
		rand.Read(buf)
		deadline := time.Now().Add(d)
		c.SetWriteDeadline(deadline.Add(2 * time.Second))
		for time.Now().Before(deadline) {
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	}
}
