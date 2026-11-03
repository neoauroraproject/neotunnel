package manage

import (
	"context"
	"fmt"
	"slices"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
	"neotunnel/internal/identity"
	"neotunnel/internal/sysinfo"
)

// Automatic encryption walks a tunnel up a ladder, from the plainest step to
// the most disguised: no encryption, each IPsec cipher, then a NeoObfs layer
// under the stack. The daemon decides when (see daemon/autocrypt.go); this is
// what a step does to the configuration.

// CryptoRung is the step a tunnel's encryption is on: "" for none, a cipher,
// or config.RungObfs.
func CryptoRung(st *config.State, name string) string {
	b, ok := st.Tunnel(Stack(st, name)[0])
	switch {
	case !ok:
		return ""
	case b.Kind == config.KindObfs:
		return config.RungObfs
	}
	return b.Encrypt
}

// CryptoRungs lists the steps a tunnel can take, plainest first. A NeoObfs
// layer needs room in the tunnel's name for "-o" and can't go under TCP.
func CryptoRungs(st *config.State, name string) []string {
	out := []string{""}
	for _, c := range config.Ciphers() {
		out = append(out, c.Name)
	}
	// ChaCha20 costs a server without AES instructions several times less,
	// so it goes before the ones that would slow it down.
	if t, ok := st.Tunnel(name); ok && !bothAES(st, *t) {
		i := slices.Index(out, "chacha20-poly1305")
		out = slices.Insert(slices.Delete(out, i, i+1), 1, "chacha20-poly1305")
	}
	top, ok1 := st.Tunnel(name)
	b, ok2 := st.Tunnel(Stack(st, name)[0])
	if ok1 && ok2 && (b.AutoLayer || (b.Kind != config.KindTCP && len(top.Name) <= 8)) {
		out = append(out, config.RungObfs)
	}
	return out
}

// hasAES is this server's AES instructions; a variable for the tests.
var hasAES = sysinfo.AESNI

// bothAES reports whether this server and the far end of t have AES
// instructions. A far server that hasn't said counts as having them, as nearly
// all do.
func bothAES(st *config.State, t config.Tunnel) bool {
	if !hasAES() {
		return false
	}
	p, ok := st.Peer(farOf(st, t))
	return !ok || p.AESNI == nil || *p.AESNI
}

// obfsAES decides whether a NeoObfs tunnel to peer runs on AES-128-GCM: both
// servers have the instructions, and the far one has told us it understands it.
func obfsAES(st *config.State, peer string) bool {
	p, ok := st.Peer(peer)
	return ok && hasAES() && p.ObfsAES && p.AESNI != nil && *p.AESNI
}

// applyRung puts a tunnel's stack on a step, adding or removing the NeoObfs
// layer as needed.
func applyRung(s *config.State, name, rung string) error {
	stack := Stack(s, name)
	b, ok := s.Tunnel(stack[0])
	if !ok {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	if rung == config.RungObfs {
		if b.Kind == config.KindObfs {
			return nil
		}
		return addAutoLayer(s, name, b)
	}
	if b.Kind == config.KindObfs {
		if !b.AutoLayer || len(stack) < 2 {
			return fmt.Errorf("%s runs over NeoObfs by itself", name)
		}
		if b, ok = dropAutoLayer(s, stack[0], stack[1]); !ok {
			return fmt.Errorf("tunnel %q changed meanwhile", name)
		}
	}
	b.Encrypt = rung
	if b.NeedsSecret() && b.Secret == "" {
		b.Secret = config.NewSecret()
	}
	return nil
}

// addAutoLayer puts a NeoObfs tunnel under b, the bottom of name's stack: the
// layer takes over the public addresses and b runs on its inner ones.
func addAutoLayer(s *config.State, name string, b *config.Tunnel) error {
	l := config.Tunnel{Name: name + "-o", Kind: config.KindObfs, AutoLayer: true, Sides: b.Sides, MTUProbe: b.MTUProbe,
		TTL: b.TTL, DSCP: b.DSCP, Padding: s.D().Padding, Pool4: b.Pool4, Pool6: b.Pool6, ObfsAES: obfsAES(s, b.Sides[1].Node)}
	if usedNames(s)[l.Name] {
		return fmt.Errorf("tunnel %q already exists", l.Name)
	}
	l.Port = FreePort(s, config.KindObfs, b.Sides[1].Node)
	if err := s.Allocate(&l); err != nil {
		return err
	}
	b.Over, b.Encrypt, b.MTUProbe = l.Name, "", false
	for i := range b.Sides {
		b.Sides[i].Endpoint, b.Sides[i].Local = "", ""
	}
	s.Tunnels = append(s.Tunnels, l)
	return nil
}

// dropAutoLayer undoes addAutoLayer and returns the stack's new bottom.
func dropAutoLayer(s *config.State, layer, next string) (*config.Tunnel, bool) {
	l, ok1 := s.Tunnel(layer)
	b, ok2 := s.Tunnel(next)
	if !ok1 || !ok2 {
		return nil, false
	}
	b.Over, b.Sides, b.MTUProbe = "", l.Sides, l.MTUProbe
	s.Tunnels = slices.DeleteFunc(s.Tunnels, func(t config.Tunnel) bool { return t.Name == layer })
	return s.Tunnel(next)
}

// SetCryptoRung moves a tunnel to a step on both servers and restarts it. Both
// ends must change together, so when the far server can't be told, this
// server goes back to the step it was on and the error says why.
func SetCryptoRung(ctx context.Context, id *identity.Identity, name, rung string) error {
	st, err := Node()
	if err != nil {
		return err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return fmt.Errorf("unknown tunnel %q", name)
	}
	was := CryptoRung(st, name)
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error { return applyRung(s, name, rung) })
	if err != nil {
		return err
	}
	for _, p := range ch.Peers {
		if !p.OK {
			if _, _, rerr := Commit(nil, func(s *config.State) error { return applyRung(s, name, was) }); rerr != nil {
				return fmt.Errorf("far server %s unreachable, and going back failed: %w", p.Peer, rerr)
			}
			return fmt.Errorf("far server %s unreachable: %s", p.Peer, p.Error)
		}
	}
	// Errors applying here may be another tunnel's; if this step can't run
	// (a kernel without the cipher), traffic stays out and the next step comes.
	return RestartTunnel(ctx, id, name)
}

// UseObfsAES moves this server's NeoObfs tunnels to peer onto AES-128-GCM once
// both ends can run it: the far server was upgraded, and both have AES
// instructions. Both ends change together (the change is pushed), and their
// links start again on the new cipher. It returns the tunnels it moved.
func UseObfsAES(peer string) ([]string, Change, error) {
	st, err := Node()
	if err != nil {
		return nil, Change{}, err
	}
	pending := func(s *config.State) []int { return obfsToAES(s, peer) }
	if len(pending(st)) == 0 {
		return nil, Change{}, nil
	}
	var moved []string
	_, ch, err := Commit([]string{peer}, func(s *config.State) error {
		moved = nil
		for _, i := range pending(s) {
			s.Tunnels[i].ObfsAES = true
			moved = append(moved, s.Tunnels[i].Name)
		}
		return nil
	})
	if err == nil {
		for _, n := range moved {
			events.Add(events.Event{Tunnel: n, Kind: events.Changed, Detail: "aes", Auto: true})
		}
	}
	return moved, ch, err
}

// obfsToAES lists this server's NeoObfs tunnels to peer still on XChaCha20 that
// can move to AES-128-GCM.
func obfsToAES(s *config.State, peer string) []int {
	if !obfsAES(s, peer) {
		return nil
	}
	var out []int
	for i, t := range s.Tunnels {
		if t.Kind == config.KindObfs && !t.ObfsAES && t.Sides[1].Node == peer {
			out = append(out, i)
		}
	}
	return out
}
