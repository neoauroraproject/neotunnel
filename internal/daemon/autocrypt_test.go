package daemon

import (
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/health"
)

var (
	dead   = health.State{Probes: 40, Grade: health.GradeDown, Loss: 100, Bad: autoAfter}
	worked = health.State{Probes: 40, Passes: true, Grade: health.GradeOK, Good: autoAfter}
)

func autoState(encrypt string) *config.State {
	return &config.State{Self: "hub", Tunnels: []config.Tunnel{
		{Name: "t1", Kind: config.KindGRE, AutoEncrypt: true, Encrypt: encrypt, Sides: [2]config.Side{{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "198.51.100.2"}}},
		{Name: "t2", Kind: config.KindGRE, Sides: [2]config.Side{{Node: "hub", Endpoint: "198.51.100.1"}, {Node: "edge", Endpoint: "198.51.100.2"}}},
	}}
}

func TestAutoCryptWalksTheLadder(t *testing.T) {
	a := newAutoCrypt()
	now := time.Unix(1_700_000_000, 0)
	hs := map[string]health.State{"t1": dead}
	st := autoState("")
	tn := st.Tunnels[0]

	to, ok := a.next(st, tn, nil, hs, now)
	if !ok || to != "aes-gcm-128" {
		t.Fatalf("first step %q %v", to, ok)
	}
	a.changed["t1"] = now
	// It waits on the step it took.
	if _, ok := a.next(st, tn, nil, hs, now.Add(autoWait-time.Second)); ok {
		t.Fatal("moved on before the wait was over")
	}
	st = autoState("aes-gcm-128")
	tn = st.Tunnels[0]
	if to, ok := a.next(st, tn, nil, hs, now.Add(autoWait)); !ok || to != "aes-gcm-256" {
		t.Fatalf("next step %q %v", to, ok)
	}
	// The settings set the wait.
	st.Defaults = &config.Defaults{AutoWait: 1}
	if _, ok := a.next(st, tn, nil, hs, now.Add(time.Minute)); !ok {
		t.Fatal("ignored the wait from the settings")
	}
}

func TestAutoCryptStaysOnWhatPasses(t *testing.T) {
	a := newAutoCrypt()
	st := autoState("aes-gcm-256")
	if _, ok := a.next(st, st.Tunnels[0], nil, map[string]health.State{"t1": worked}, time.Now()); ok {
		t.Fatal("moved while traffic passes")
	}
	// A bad round or two isn't reason enough.
	one := dead
	one.Bad = autoAfter - 1
	if _, ok := a.next(st, st.Tunnels[0], nil, map[string]health.State{"t1": one}, time.Now()); ok {
		t.Fatal("moved too soon")
	}
}

// A tunnel whose forwards have a backup that works is only watched; with no
// forward, or a forward with nowhere to go, it takes the next step.
func TestAutoCryptLeavesTunnelsWithABackup(t *testing.T) {
	a := newAutoCrypt()
	st := autoState("")
	tn := st.Tunnels[0]
	now := time.Now()
	withBackup := []config.Forward{{Node: "hub", Tunnel: "t1", Backup: "t2", Ports: "443", Proto: "tcp"}}
	if _, ok := a.next(st, tn, withBackup, map[string]health.State{"t1": dead, "t2": worked}, now); ok {
		t.Fatal("changed encryption although the forward can run on its backup")
	}
	if _, ok := a.next(st, tn, withBackup, map[string]health.State{"t1": dead, "t2": dead}, now); !ok {
		t.Fatal("no working backup: it should try the next step")
	}
	also := append(withBackup, config.Forward{Node: "hub", Tunnel: "t1", Ports: "80", Proto: "tcp"})
	if _, ok := a.next(st, tn, also, map[string]health.State{"t1": dead, "t2": worked}, now); !ok {
		t.Fatal("a forward with no backup is cut off: it should try the next step")
	}
}

// After every step failed in a row the ladder rests before it starts over,
// and traffic passing starts a new ladder.
func TestAutoCryptRestsAfterAFullRound(t *testing.T) {
	a := newAutoCrypt()
	now := time.Unix(1_700_000_000, 0)
	hs := map[string]health.State{"t1": dead}
	st := autoState("")
	if _, ok := a.next(st, st.Tunnels[0], nil, hs, now); !ok {
		t.Fatal("first step")
	}
	last := autoState("chacha20-poly1305") // the last cipher: NeoObfs is next
	if to, ok := a.next(last, last.Tunnels[0], nil, hs, now); !ok || to != config.RungObfs {
		t.Fatalf("got %q %v", to, ok)
	}
	a.changed["t1"] = now
	// On NeoObfs (its layer below) the next step is the one it started on.
	st = autoState("")
	st.Tunnels = append(st.Tunnels, config.Tunnel{Name: "t1-o", Kind: config.KindObfs, AutoLayer: true, Sides: st.Tunnels[0].Sides})
	st.Tunnels[0].Over = "t1-o"
	tn := st.Tunnels[0]
	if _, ok := a.next(st, tn, nil, hs, now.Add(autoWait)); ok {
		t.Fatal("started over without resting")
	}
	if to, ok := a.next(st, tn, nil, hs, now.Add(autoRest)); !ok || to != "" {
		t.Fatalf("after the rest %q %v", to, ok)
	}
	a.next(st, tn, nil, map[string]health.State{"t1": worked}, now)
	if _, seen := a.start["t1"]; seen {
		t.Fatal("passing traffic should end the ladder")
	}
}
