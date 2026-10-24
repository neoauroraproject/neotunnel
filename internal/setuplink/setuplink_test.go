package setuplink

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	config.Dir = t.TempDir()
	id, err := identity.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func payload(rev uint64) Payload {
	return Payload{
		Master:   config.MasterRef{Name: "hub", Address: "198.51.100.1:7710"},
		Peer:     "edge",
		Revision: rev,
		Tunnels:  []config.Tunnel{{Name: "t1", Kind: config.KindSIT}},
	}
}

func TestRoundTrip(t *testing.T) {
	id := newID(t)
	key, err := Encode(id, payload(3))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(key)
	if err != nil {
		t.Fatal(err)
	}
	if p.Peer != "edge" || p.Revision != 3 || p.Master.PublicKey != id.PublicString() || len(p.Tunnels) != 1 {
		t.Errorf("payload mangled: %+v", p)
	}
}

func TestTamperDetected(t *testing.T) {
	id := newID(t)
	key, _ := Encode(id, payload(1))
	body, sig, _ := strings.Cut(strings.TrimPrefix(key, Prefix), ".")
	other, _ := Encode(newID(t), payload(1))
	_, otherSig, _ := strings.Cut(strings.TrimPrefix(other, Prefix), ".")

	if _, err := Decode(Prefix + body + "." + otherSig); err == nil {
		t.Error("signature from another key accepted")
	}
	if _, err := Decode(Prefix + body[:len(body)-4] + "." + sig); err == nil {
		t.Error("truncated setup key accepted")
	}
}

func TestAcceptRules(t *testing.T) {
	id := newID(t)
	st := &config.State{}
	l1, _ := Encode(id, payload(5))
	p1, _ := Decode(l1)
	if err := Accept(st, p1, false); err != nil {
		t.Fatal(err)
	}
	if st.Self != "edge" || st.Managed.Revision != 5 {
		t.Fatalf("not applied: %+v", st.Managed)
	}

	l0, _ := Encode(id, payload(4))
	p0, _ := Decode(l0)
	if Accept(st, p0, false) == nil {
		t.Error("older revision accepted")
	}

	// Another main server adds itself next to the first; its key updates
	// only its own part.
	p2 := payload(9)
	p2.Master.Name, p2.Peer = "hub2", "tr"
	other, _ := Encode(newID(t), p2)
	po, _ := Decode(other)
	if err := Accept(st, po, false); err != nil {
		t.Fatalf("second master refused: %v", err)
	}
	if ms := st.Masters(); len(ms) != 2 || ms[0].Revision != 5 || ms[1].Revision != 9 || ms[1].Self != "tr" || !st.IsSelf("tr") || !st.IsSelf("edge") {
		t.Fatalf("masters %+v", ms)
	}
	if Accept(st, p1, false) != nil || len(st.Masters()) != 2 {
		t.Error("the first master's key again should update its part only")
	}

	// A master with a server named like this one would confuse the sides.
	p3 := payload(1)
	p3.Master.Name = "tr"
	clash, _ := Encode(newID(t), p3)
	pc, _ := Decode(clash)
	if Accept(st, pc, false) == nil {
		t.Error("master named like this server accepted")
	}

	if Accept(st, po, true) != nil || len(st.Masters()) != 1 || st.Managed.Master.Name != "hub2" {
		t.Error("--force should leave only the new master")
	}
}

// Both servers installed on hosts called the same: the destination, which
// runs no tunnels of its own, takes the name the master gives it.
func TestSameNameAsMaster(t *testing.T) {
	id := newID(t)
	p := payload(1)
	p.Master.Name = "hmray"
	k, _ := Encode(id, p)
	pd, _ := Decode(k)

	st := &config.State{Self: "hmray"}
	if err := Accept(st, pd, false); err != nil {
		t.Fatalf("refused: %v", err)
	}
	if st.Self != "edge" || st.Label != "hmray" || st.IsSelf("hmray") {
		t.Fatalf("self %q label %q", st.Self, st.Label)
	}

	// With tunnels of its own under that name, it can't give it up.
	st = &config.State{Self: "hmray", Tunnels: []config.Tunnel{{Name: "own", Kind: config.KindGRE}}}
	if Accept(st, pd, false) == nil {
		t.Fatal("a main server with its own tunnels gave up its name")
	}
}
