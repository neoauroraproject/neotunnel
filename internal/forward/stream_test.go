package forward

import (
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/relay"
)

// A relay forward runs without the tunnel's interface: the entry relays to
// the far node's public address, and the far node (which gets the forward
// with its configuration) accepts it for the forward's ports only.
func TestRelayModeEntryAndExit(t *testing.T) {
	hub := modeState()
	hub.Tunnels[0].Sides[1].Endpoint = "203.0.113.9"
	hub.Tunnels[0].Encrypt = "aes-gcm-256"
	hub.Tunnels[0].Profile = config.ProfileMax
	hub.Tunnels[0].Secret = config.NewSecret()
	hub.Forwards = []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "443,8443", To: 8080, Mode: config.ForwardRelay}}
	_, own := hub.Effective()
	rs, errs := Build(hub, own, nil) // the tunnel need not be up
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(rs.Rules) != 0 || len(rs.Relays) != 2 || len(rs.Exits) != 0 {
		t.Fatalf("entry: %d rules, %d relays, %d exits", len(rs.Rules), len(rs.Relays), len(rs.Exits))
	}
	r := rs.Relays[1]
	want := relay.Via{Tunnel: "t1", Secret: hub.Tunnels[0].Secret, Side: 0, Cipher: "aes-gcm-256", Pool: 8}
	got := r.Via
	if got.Exit.String() != "203.0.113.9:7712" || r.Port != 8443 || r.Target.Port() != 8080 || r.Version != 0 {
		t.Errorf("relay %+v", r)
	}
	if got.Inside.String() != "10.77.0.2:7712" {
		t.Errorf("fallback inside the tunnel: %v", got.Inside)
	}
	got.Exit, got.Inside = want.Exit, want.Inside
	if got != want {
		t.Errorf("via %+v", r.Via)
	}

	ts, fs := hub.ForPeer("edge")
	edge := &config.State{Self: "edge", Managed: &config.Managed{Self: "edge", Tunnels: ts, Forwards: fs}}
	_, own = edge.Effective()
	rs, errs = Build(edge, own, nil)
	if len(errs) > 0 || len(rs.Relays) != 0 || len(rs.Exits) != 1 {
		t.Fatalf("exit: %v, %d relays, %+v", errs, len(rs.Relays), rs.Exits)
	}
	if e := rs.Exits[0]; e.Tunnel != "t1" || e.Side != 1 || len(e.Ports) != 1 || e.Ports[0] != 8080 {
		t.Errorf("exit %+v", e)
	}
}

func TestRelayModeNeedsSecretAndTCP(t *testing.T) {
	st := modeState()
	st.Tunnels[0].Sides[1].Endpoint = "203.0.113.9"
	fs := []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "443", Mode: config.ForwardRelay}}
	if _, errs := Build(st, fs, nil); len(errs) != 1 {
		t.Error("relay without a tunnel secret accepted")
	}
	if config.ValidForwardMode(config.ForwardRelay, 0, "both") == nil {
		t.Error("relay mode accepted UDP")
	}
}

// A socks forward listens on loopback (or Listen) and relays through the
// tunnel's inner address; the far node gets it and opens its exit for
// egress, without ports.
func TestSocksModeEntryAndExit(t *testing.T) {
	hub := modeState()
	hub.Tunnels[0].Secret = config.NewSecret()
	hub.Forwards = []config.Forward{{Node: "hub", Tunnel: "t1", Proto: "tcp", Ports: "1080", Mode: config.ForwardSocks, Auth: "a:b"}}
	_, own := hub.Effective()
	rs, errs := Build(hub, own, nil)
	if len(errs) > 0 || len(rs.Relays) != 1 || len(rs.Rules) != 0 {
		t.Fatalf("entry: %v %+v", errs, rs)
	}
	r := rs.Relays[0]
	if !r.Socks || r.Port != 1080 || r.Listen.String() != "127.0.0.1" || r.Auth != "a:b" || r.Via.Exit.String() != "10.77.0.2:7712" {
		t.Errorf("proxy %+v", r)
	}
	ts, fs := hub.ForPeer("edge")
	edge := &config.State{Self: "edge", Managed: &config.Managed{Self: "edge", Tunnels: ts, Forwards: fs}}
	_, own = edge.Effective()
	rs, errs = Build(edge, own, nil)
	if len(errs) > 0 || len(rs.Exits) != 1 || !rs.Exits[0].Egress || len(rs.Exits[0].Ports) != 0 {
		t.Fatalf("exit: %v %+v", errs, rs.Exits)
	}
}
