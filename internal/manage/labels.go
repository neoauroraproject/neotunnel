package manage

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
)

// MaxLabel is the longest display name, in characters.
const MaxLabel = 40

// CleanLabel trims a display name and checks it: any script, spaces
// inside, up to MaxLabel characters, no control characters. A label equal
// to the ID is stored empty, so the ID shows.
func CleanLabel(label, id string) (string, error) {
	label = strings.Join(strings.Fields(label), " ")
	if utf8.RuneCountInString(label) > MaxLabel {
		return "", fmt.Errorf("display name must be at most %d characters", MaxLabel)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("display name has a control character")
		}
	}
	if label == id {
		label = ""
	}
	return label, nil
}

// SetTunnelLabel renames a tunnel for display. Its name stays its ID, so
// the running tunnel is untouched; the far end gets the label too.
func SetTunnelLabel(name, label string) (Change, error) {
	label, err := CleanLabel(label, name)
	if err != nil {
		return Change{}, err
	}
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return Change{}, fmt.Errorf("unknown tunnel %q", name)
	}
	_, ch, err := Commit(st.PeersOf(*t), func(s *config.State) error {
		top, ok := s.Tunnel(name)
		if !ok {
			return fmt.Errorf("tunnel %q changed meanwhile", name)
		}
		top.Label = label
		return nil
	})
	if err == nil {
		events.Add(events.Event{Tunnel: name, Kind: events.Changed, Detail: "label"})
	}
	return ch, err
}

// SetPeerLabel renames a peer for display on this node.
func SetPeerLabel(name, label string) error {
	label, err := CleanLabel(label, name)
	if err != nil {
		return err
	}
	_, err = config.Update(func(s *config.State) error {
		p, ok := s.Peer(name)
		if !ok {
			return fmt.Errorf("unknown server %q", name)
		}
		p.Label = label
		return nil
	})
	return err
}

// SetSelfLabel renames this node for display. Unlike its name (SetNode),
// it can change while tunnels exist.
func SetSelfLabel(label string) error {
	if _, err := Node(); err != nil {
		return err
	}
	_, err := config.Update(func(s *config.State) error {
		l, err := CleanLabel(label, s.Self)
		if err != nil {
			return err
		}
		s.Label = l
		return nil
	})
	return err
}
