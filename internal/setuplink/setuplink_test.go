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
		Master:   config.MasterRef{Name: "iran", Address: "198.51.100.1:7710"},
		Peer:     "kharej",
		Revision: rev,
		Tunnels:  []config.Tunnel{{Name: "t1", Kind: config.KindSIT}},
	}
}

func TestRoundTrip(t *testing.T) {
	id := newID(t)
	link, err := Encode(id, payload(3))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(link)
	if err != nil {
		t.Fatal(err)
	}
	if p.Peer != "kharej" || p.Revision != 3 || p.Master.PublicKey != id.PublicString() || len(p.Tunnels) != 1 {
		t.Errorf("payload mangled: %+v", p)
	}
}

func TestTamperDetected(t *testing.T) {
	id := newID(t)
	link, _ := Encode(id, payload(1))
	body, sig, _ := strings.Cut(strings.TrimPrefix(link, Scheme), ".")
	other, _ := Encode(newID(t), payload(1))
	_, otherSig, _ := strings.Cut(strings.TrimPrefix(other, Scheme), ".")

	if _, err := Decode(Scheme + body + "." + otherSig); err == nil {
		t.Error("signature from another key accepted")
	}
	if _, err := Decode(Scheme + body[:len(body)-4] + "." + sig); err == nil {
		t.Error("truncated link accepted")
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
	if st.Self != "kharej" || st.Managed.Revision != 5 {
		t.Fatalf("not applied: %+v", st.Managed)
	}

	l0, _ := Encode(id, payload(4))
	p0, _ := Decode(l0)
	if Accept(st, p0, false) == nil {
		t.Error("older revision accepted")
	}

	other, _ := Encode(newID(t), payload(9))
	po, _ := Decode(other)
	if Accept(st, po, false) == nil {
		t.Error("link from a different master accepted without --force")
	}
	if Accept(st, po, true) != nil {
		t.Error("--force should switch masters")
	}
}
