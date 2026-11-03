// Package transport carries connection tunnels (the stream-* kinds): a few
// long-lived sessions between two servers, each multiplexing many streams
// (smux), one stream per user connection. Nothing travels as packets and
// nothing touches the kernel's interfaces or routes: the user's TCP ends at
// the entry server and starts again at the exit, and only its bytes cross.
//
// One side listens and the other connects. Which is which is the tunnel's
// choice (Spec.Dial), not the direction of the traffic: with the far server
// connecting in (like FRP) a path that only lets connections in still
// carries forwards both ways, since either side opens streams on a session.
//
// A session starts like a relay connection (relay.RelayPort's protocol):
//
//	plain     "NTS" version(1) len(1) tunnel len(1) cipher, then tcptun's
//	          challenge-response under purpose "session tcp <cipher>"
//	TLS       a TLS handshake with a browser's ClientHello, then the same
//	          preamble with cipher "" and purpose "session tls <ekm>", where
//	          ekm is the TLS connection's exported keying material
//
// so only the tunnel's other end gets a session, and on TLS a machine in
// the middle that ends TLS on each side (and so has different keys on each
// leg) fails the handshake instead of reading the tunnel. Whoever does not
// hold the secret meets a plain web server. Each stream then starts with a
// type byte: streamRelay (a relay request follows, see internal/relay),
// streamPing (echoed, for the health probes) or streamProbe (carried to
// this server's probe server, for speed tests: a connection tunnel has no
// inner address the test could reach it on).
//
// Several sessions run at once (Spec.Conns) and each new stream goes to the
// least busy one: one session's lost packet then holds back only the users
// on it. Every change that needs new sessions brings them up before the old
// ones go, and the old ones stay until their users' connections end.
package transport

import (
	"net/netip"
	"time"

	"github.com/xtaci/smux"

	"neotunnel/internal/config"
)

// Spec is one connection tunnel's end on this server.
type Spec struct {
	Name   string // the tunnel's name here
	Tunnel string // the name both ends derive keys from (KeyName)
	Secret string
	Side   int
	Kind   config.Kind // KindStreamTCP or KindStreamTLS
	// Dial: this side connects to Remote; otherwise it listens on Ports.
	Dial   bool
	Remote netip.Addr
	// Ports are tried in order until one connects (zero = unused).
	Ports    [config.MaxRelayPorts]uint16
	Conns    int
	SNI      string
	Cipher   string // stream-tcp only: tcptun's cipher, "" = authenticated only
	RateMbit int    // what this side sends, 0 = no limit
}

// PortList is Ports without the unused ones.
func (s Spec) PortList() []uint16 {
	var out []uint16
	for _, p := range s.Ports {
		if p != 0 {
			out = append(out, p)
		}
	}
	return out
}

// sameSessions reports whether a and b need the same sessions (only the
// speed limit may differ, and it changes in place).
func sameSessions(a, b Spec) bool {
	a.RateMbit, b.RateMbit = 0, 0
	return a == b
}

const (
	version = 1

	streamRelay = 0
	streamPing  = 1
	streamProbe = 2

	dialTimeout      = 10 * time.Second
	fallbackTimeout  = 4 * time.Second // per port while another remains
	handshakeTimeout = 10 * time.Second
	maxBackoff       = 30 * time.Second
	// openWait is how long a new user waits for a session to come up.
	openWait = 5 * time.Second
	// drainMax is how long the sessions a change replaced keep carrying the
	// connections they had.
	drainMax = 10 * time.Minute
	// notSentLowat caps the unsent bytes each session socket queues beyond
	// what is in flight, so streams sharing it don't wait behind a long
	// queue (see tcptun.Tune).
	notSentLowat = 256 << 10
)

// smuxConfig: protocol 2 gives each stream its own window, so one slow user
// can't fill the session's buffer and stall the others. Frames of 16 KiB
// keep the mux's cost (8 bytes a frame) under 0.1%.
func smuxConfig() *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2
	c.MaxFrameSize = 16 << 10
	c.MaxReceiveBuffer = 32 << 20
	c.MaxStreamBuffer = 4 << 20
	c.KeepAliveInterval = 10 * time.Second
	c.KeepAliveTimeout = 40 * time.Second
	return c
}
