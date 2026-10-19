package safe

import (
	"context"
	"errors"
	"testing"
	"time"

	"neotunnel/internal/config"
)

// fake records what the hooks were asked to do.
type fake struct {
	confirms map[string]error // peer -> answer
	asked    []string
	reverted *Snapshot
	revertTo []string
}

func (f *fake) hooks() Hooks {
	return Hooks{
		ConfirmPeer: func(_ context.Context, peer, _ string) error {
			f.asked = append(f.asked, peer)
			return f.confirms[peer]
		},
		Revert: func(prev Snapshot, peers []string) error {
			f.reverted, f.revertTo = &prev, peers
			return nil
		},
	}
}

func before() *config.State {
	return &config.State{Self: "ir", Tunnels: []config.Tunnel{{Name: "t1", MTU: 1400}},
		Forwards: []config.Forward{{Node: "ir", Tunnel: "t1", Ports: "443"}}}
}

func TestUnconfirmedChangeIsReverted(t *testing.T) {
	config.Dir = t.TempDir()
	now := time.Now()
	p, err := Begin(before(), "tunnel t1 settings", now)
	if err != nil {
		t.Fatal(err)
	}
	SetPeers(p.ID, []string{"de"})
	if _, err := Begin(before(), "another", now); !errors.Is(err, ErrBusy) {
		t.Fatalf("second change while one is pending: %v", err)
	}
	f := &fake{}
	if o := Tick(context.Background(), now.Add(Wait-time.Second), f.hooks()); o != nil || f.reverted != nil {
		t.Fatalf("reverted before the deadline: %+v", o)
	}
	o := Tick(context.Background(), now.Add(Wait+time.Second), f.hooks())
	if o == nil || o.Kept || f.reverted == nil {
		t.Fatalf("not reverted after the deadline: %+v", o)
	}
	if f.reverted.Tunnels[0].MTU != 1400 || len(f.reverted.Forwards) != 1 || f.revertTo[0] != "de" {
		t.Fatalf("reverted to %+v on %v", f.reverted, f.revertTo)
	}
	if Current() != nil || Last() == nil || Last().Kept || Last().Reason == "" {
		t.Fatalf("after revert: pending %+v, last %+v", Current(), Last())
	}
	if len(f.asked) != 0 {
		t.Fatalf("peers asked to confirm a change the panel never kept: %v", f.asked)
	}
}

func TestKeptOnlyOncePeersConfirm(t *testing.T) {
	config.Dir = t.TempDir()
	now := time.Now()
	p, _ := Begin(before(), "x", now)
	SetPeers(p.ID, []string{"de", "nl"})
	if err := Keep(p.ID); err != nil {
		t.Fatal(err)
	}
	f := &fake{confirms: map[string]error{"nl": errors.New("unreachable")}}
	if o := Tick(context.Background(), now.Add(time.Second), f.hooks()); o != nil {
		t.Fatalf("kept while nl hasn't confirmed: %+v", o)
	}
	// de confirmed already and isn't asked again.
	f.asked = nil
	f.confirms["nl"] = nil
	o := Tick(context.Background(), now.Add(2*time.Second), f.hooks())
	if o == nil || !o.Kept || f.reverted != nil {
		t.Fatalf("not kept: %+v", o)
	}
	if len(f.asked) != 1 || f.asked[0] != "nl" {
		t.Fatalf("asked %v", f.asked)
	}
}

func TestKeptButPeerSilentIsReverted(t *testing.T) {
	config.Dir = t.TempDir()
	now := time.Now()
	p, _ := Begin(before(), "x", now)
	SetPeers(p.ID, []string{"de"})
	Keep(p.ID)
	f := &fake{confirms: map[string]error{"de": errors.New("timeout")}}
	o := Tick(context.Background(), now.Add(Wait+time.Second), f.hooks())
	if o == nil || o.Kept || f.reverted == nil || o.Reason != "de did not confirm in time" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestUndoAndCancel(t *testing.T) {
	config.Dir = t.TempDir()
	p, _ := Begin(before(), "x", time.Now())
	f := &fake{}
	if o, err := RevertNow(p.ID, f.hooks()); err != nil || o == nil || f.reverted == nil {
		t.Fatalf("undo: %v %+v", err, o)
	}
	if _, err := RevertNow(p.ID, f.hooks()); err == nil {
		t.Fatal("undid twice")
	}
	p, _ = Begin(before(), "y", time.Now())
	Cancel(p.ID)
	if Current() != nil {
		t.Fatal("cancel left the change pending")
	}
}

func TestHoldFor(t *testing.T) {
	config.Dir = t.TempDir()
	now := time.Now()
	if id, _ := HoldFor(now); id != "" {
		t.Fatal("hold without a pending change")
	}
	p, _ := Begin(before(), "x", now)
	id, secs := HoldFor(now.Add(10 * time.Second))
	if id != p.ID || secs != int((Wait-10*time.Second+PeerGrace)/time.Second) {
		t.Fatalf("hold %s %d", id, secs)
	}
	if id, _ := HoldFor(now.Add(Wait)); id != "" {
		t.Fatal("hold after the deadline")
	}
}

func TestPeerHold(t *testing.T) {
	config.Dir = t.TempDir()
	now := time.Now()
	m := &config.Managed{Revision: 7, Tunnels: []config.Tunnel{{Name: "t1", MTU: 1400}}}
	if err := HoldManaged("c1", 75, &config.State{Managed: m}, m, now); err != nil {
		t.Fatal(err)
	}
	// The master's change lands; a second push of the same change keeps the
	// first snapshot.
	m.Revision, m.Tunnels[0].MTU = 8, 1280
	HoldManaged("c1", 70, &config.State{Managed: m}, m, now)
	if h := ExpiredHold(now.Add(69 * time.Second)); h != nil {
		t.Fatal("expired early")
	}
	h := ExpiredHold(now.Add(71 * time.Second))
	if h == nil {
		t.Fatal("hold did not expire")
	}
	RestoreManaged(m, h)
	if m.Revision != 7 || m.Tunnels[0].MTU != 1400 {
		t.Fatalf("restored %+v", m)
	}
	if ExpiredHold(now.Add(time.Hour)) != nil {
		t.Fatal("hold not dropped after it expired")
	}

	// Confirmed: nothing to put back.
	HoldManaged("c2", 75, &config.State{Managed: m}, m, now)
	if ConfirmHold("other") || !ConfirmHold("c2") || ExpiredHold(now.Add(time.Hour)) != nil {
		t.Fatal("confirm")
	}
	// A settled push (no hold) drops any hold.
	HoldManaged("c3", 75, &config.State{Managed: m}, m, now)
	HoldManaged("", 0, &config.State{Managed: m}, m, now)
	if ConfirmHold("c3") {
		t.Fatal("hold survived a settled push")
	}
}
