package manage

import (
	"fmt"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
)

// Rollback puts the network back as it was before the history entry id
// (see config.History), here and on every paired server. Tunnels another
// server manages here are its own and stay. The rollback is itself a change
// in the history, so it can be undone the same way.
func Rollback(id string) (Change, error) {
	snap, net, err := config.ReadSnapshot(id)
	if err != nil {
		return Change{}, err
	}
	st, err := Node()
	if err != nil {
		return Change{}, err
	}
	var peers []string
	for _, p := range st.Peers {
		if p.Paired() {
			peers = append(peers, p.Name)
		}
	}
	_, ch, err := commit(peers, func(s *config.State) error {
		net.Put(s)
		return nil
	}, id)
	if err == nil {
		events.Add(events.Event{Kind: events.Changed, Detail: fmt.Sprintf("rollback %d", snap.Revision)})
	}
	return ch, err
}
