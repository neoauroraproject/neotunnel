package engine

import (
	"fmt"
	"net/netip"

	"neotunnel/internal/config"
	"neotunnel/internal/transport"
)

// Connection tunnels (the stream-* kinds) have no interface: the daemon's
// data plane runs their sessions (internal/transport) and their forwards
// are relays over those sessions.

// StreamSpecs are this node's ends of the running connection tunnels.
func StreamSpecs(st *config.State) ([]transport.Spec, []error) {
	ts, _ := st.Effective()
	var out []transport.Spec
	var errs []error
	for _, t := range Active(ts) {
		if !t.IsStream() {
			continue
		}
		s, err := streamSpec(st.IsSelf, t)
		if err != nil {
			errs = append(errs, fmt.Errorf("tunnel %s: %w", t.Name, err))
			continue
		}
		out = append(out, s)
	}
	return out, errs
}

func streamSpec(isSelf func(string) bool, t config.Tunnel) (transport.Spec, error) {
	me, err := sideIndex(isSelf, t)
	if err != nil {
		return transport.Spec{}, err
	}
	s := transport.Spec{Name: t.Name, Tunnel: t.KeyName(), Secret: t.Secret, Side: me, Kind: t.Kind,
		Dial: me != t.Listener(), Conns: t.StreamConns(), SNI: t.SNI, RateMbit: t.RateMbit}
	if t.Secret == "" {
		return s, fmt.Errorf("no secret; save the tunnel again")
	}
	if t.Kind == config.KindStreamTCP && t.Encrypt != "" {
		if _, err := config.LookupCipher(t.Encrypt); err != nil {
			return s, err
		}
		s.Cipher = t.Encrypt
	}
	ports := t.StreamPorts()
	if len(ports) == 0 {
		return s, fmt.Errorf("no port")
	}
	copy(s.Ports[:], ports)
	if a, err := netip.ParseAddr(t.Sides[1-me].Endpoint); err == nil {
		s.Remote = a.Unmap()
	} else if s.Dial {
		return s, fmt.Errorf("far end %q is not an IP address", t.Sides[1-me].Endpoint)
	}
	return s, nil
}

// StreamListenPorts are the TCP ports this node's connection tunnels listen
// on (for the firewall).
func StreamListenPorts(specs []transport.Spec) []uint16 {
	var out []uint16
	for _, s := range specs {
		if !s.Dial {
			out = append(out, s.PortList()...)
		}
	}
	return out
}

// StreamDetail is a connection tunnel's sessions and streams now.
func StreamDetail(tunnel string) (transport.State, bool) {
	us := currentUserspace()
	if us == nil {
		return transport.State{}, false
	}
	return us.StreamState(tunnel)
}

// StreamState is a connection tunnel's live state from the data plane: Up
// while a session is open, with the bytes on the wire.
func StreamState(tunnel string) (LinkState, bool) {
	us := currentUserspace()
	if us == nil {
		return LinkState{}, false
	}
	st, ok := us.StreamState(tunnel)
	if !ok {
		return LinkState{}, false
	}
	return LinkState{Exists: true, Up: st.Sessions > 0, RxBytes: st.Rx, TxBytes: st.Tx}, true
}
