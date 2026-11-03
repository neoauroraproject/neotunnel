package daemon

import (
	"context"
	"log"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
	"neotunnel/internal/fleet"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/version"
)

// Automatic upgrades: once this server runs a newer release than a paired
// server, it upgrades that server, one at a time, at most one per peer
// watch. A server that doesn't answer on the new version goes back to its
// old one by itself (package upgrade) and says so; that version isn't
// tried on it again, and the other servers wait for the next release.

var (
	// halted is the version whose upgrades stopped after a failure.
	halted string
	// toldBack are the rollbacks already announced, by peer and version.
	toldBack = map[string]bool{}
)

// upgradeWanted reports whether a paired peer should get this version now.
func upgradeWanted(p config.Peer, mine string, now time.Time) bool {
	return p.Paired() && p.Version != "" && version.Newer(mine, p.Version) &&
		p.RolledBack != mine && now.Sub(p.LastSeen) < 3*time.Minute
}

func autoUpgrade(ctx context.Context, id *identity.Identity) {
	st, err := config.Load()
	if err != nil {
		return
	}
	mine := version.Version
	for _, p := range st.Peers {
		if p.RolledBack != "" && !toldBack[p.Name+" "+p.RolledBack] {
			toldBack[p.Name+" "+p.RolledBack] = true
			if p.RolledBack == mine {
				log.Printf("upgrade of %s to %s was undone there: it didn't answer in time", p.Name, mine)
				events.Add(events.Event{Kind: events.Upgrade, To: mine, From: p.Name, Detail: "rolled back", Auto: true})
			}
		}
	}
	if st.D().NoAutoUpgrade || !version.Release(mine) || halted == mine {
		return
	}
	for _, p := range st.Peers {
		if !upgradeWanted(p, mine, time.Now()) {
			continue
		}
		log.Printf("upgrading %s from %s to %s", p.Name, p.Version, mine)
		uctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		v, err := fleet.Upgrade(uctx, id, st, p.Name)
		cancel()
		if err == nil && v == mine {
			log.Printf("%s now runs %s", p.Name, mine)
			events.Add(events.Event{Kind: events.Upgrade, To: mine, From: p.Name, Detail: "ok", Auto: true})
			manage.UseObfsAES(p.Name)
			return
		}
		// Not back on the new version: its watchdog decides, and the
		// others wait for the next release.
		halted = mine
		why := "it didn't come back"
		if err != nil {
			why = err.Error()
		}
		log.Printf("upgrade of %s to %s: %s; no more automatic upgrades to %s", p.Name, mine, why, mine)
		events.Add(events.Event{Kind: events.Upgrade, To: mine, From: p.Name, Detail: "failed: " + why, Auto: true})
		return
	}
}
