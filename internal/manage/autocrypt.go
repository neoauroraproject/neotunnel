package manage

import (
	"context"
	"fmt"
	"slices"

	"neotunnel/internal/config"
	"neotunnel/internal/identity"
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
	top, ok1 := st.Tunnel(name)
	b, ok2 := st.Tunnel(Stack(st, name)[0])
	if ok1 && ok2 && (b.AutoLayer || (b.Kind != config.KindTCP && len(top.Name) <= 8)) {
		out = append(out, config.RungObfs)
	}
	return out
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
		TTL: b.TTL, DSCP: b.DSCP, Padding: s.D().Padding, Pool4: b.Pool4, Pool6: b.Pool6}
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
