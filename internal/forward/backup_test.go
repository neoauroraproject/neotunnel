package forward

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/failover"
)

// backupState has two tunnels between hub and edge: t1 and its backup t2.
func backupState(mode string) *config.State {
	st := modeState()
	st.Tunnels = append(st.Tunnels, config.Tunnel{
		Name: "t2", Kind: config.KindGRE, Net4: "10.77.0.4/30",
		Sides: [2]config.Side{{Node: "hub"}, {Node: "edge", Endpoint: "203.0.113.9"}},
	})
	st.Tunnels[0].Sides[1].Endpoint = "203.0.113.9"
	for i := range st.Tunnels {
		st.Tunnels[i].Secret = config.NewSecret()
	}
	st.Forwards = []config.Forward{{Node: "hub", Tunnel: "t1", Backup: "t2", Proto: "tcp", Ports: "443", Mode: mode}}
	return st
}

// While failed over, the forward's DNAT goes to the backup's far end.
func TestFailedOverForwardTargetsBackup(t *testing.T) {
	st := backupState("")
	links := []Link{{Name: "nt-t1", Tunnel: "t1"}, {Name: "nt-t2", Tunnel: "t2"}}
	ts, fs := st.Effective()
	rs, errs := Build(st, failover.Route(fs, ts, failover.Paths{}), links)
	if len(errs) > 0 || len(rs.Rules) != 1 || rs.Rules[0].Iface != "nt-t1" {
		t.Fatalf("primary: %v %+v", errs, rs.Rules)
	}
	ps := failover.Paths{failover.Key(fs[0]): {Active: "t2"}}
	rs, errs = Build(st, failover.Route(fs, ts, ps), links)
	if len(errs) > 0 || len(rs.Rules) != 1 || rs.Rules[0].Iface != "nt-t2" {
		t.Fatalf("backup: %v %+v", errs, rs.Rules)
	}
	s := Script(rs)
	for _, want := range []string{"tcp dport { 443 } dnat ip to 10.77.0.6", `oifname "nt-t2" ct status dnat masquerade`} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// The counters stay the forward's, whichever tunnel carries it.
	if rs.Rules[0].Key != "tcp/443" {
		t.Errorf("key %q", rs.Rules[0].Key)
	}
}

// The far end can't tell when the near end fails over, so it keeps a return
// path (transparent) or an exit (relay) on both tunnels.
func TestFarEndServesBothTunnels(t *testing.T) {
	for _, mode := range []string{config.ForwardTransparent, config.ForwardRelay} {
		hub := backupState(mode)
		ts, fs := hub.ForPeer("edge")
		if len(fs) != 1 {
			t.Fatalf("%s: forward not sent to the far end", mode)
		}
		edge := &config.State{Self: "edge", Managed: &config.Managed{Self: "edge", Tunnels: ts, Forwards: fs}}
		_, own := edge.Effective()
		rs, errs := Build(edge, own, []Link{{Name: "nt-t1", Tunnel: "t1"}, {Name: "nt-t2", Tunnel: "t2"}})
		if len(errs) > 0 {
			t.Fatalf("%s: %v", mode, errs)
		}
		switch mode {
		case config.ForwardTransparent:
			if len(rs.Returns) != 2 || rs.Returns[1].Iface != "nt-t2" {
				t.Errorf("returns %+v", rs.Returns)
			}
		case config.ForwardRelay:
			if len(rs.Exits) != 2 || rs.Exits[1].Tunnel != "t2" || rs.Exits[1].Ports[0] != 443 {
				t.Errorf("exits %+v", rs.Exits)
			}
		}
	}
}

// A peer gets a transparent forward even when only its backup tunnel runs
// there.
func TestForPeerFollowsBackup(t *testing.T) {
	hub := backupState(config.ForwardTransparent)
	hub.Tunnels[1].Sides[1].Node = "other"
	hub.Forwards[0].Tunnel = "t2"
	hub.Forwards[0].Backup = "t1"
	if _, fs := hub.ForPeer("edge"); len(fs) != 1 {
		t.Fatalf("edge should get the forward its backup runs to")
	}
}
