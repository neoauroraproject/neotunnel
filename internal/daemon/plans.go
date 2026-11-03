package daemon

import (
	"log"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
	"neotunnel/internal/forward"
	"neotunnel/internal/traffic"
)

// enforcePlans keeps each sold port's quota period counting from its start,
// closes the ports that are paused, past their end or out of traffic, and
// opens them again once that changes (renewed, more quota, a new period).
// The speed cap and the client limits need no watching: the kernel holds
// them (see forward.Plan).
func enforcePlans(fs []config.Forward, store *traffic.Store, now time.Time, changed func()) {
	closed := map[string]string{}
	for _, f := range fs {
		p := f.Plan
		if p == nil {
			continue
		}
		name := traffic.Port + f.PortKey()
		from := p.PeriodStart(now.Unix())
		store.StartPeriod(name, from, now)
		if why := planClosed(*p, store, name, from, now); why != "" {
			closed[f.PortKey()] = why
		}
	}
	diff := false
	for _, f := range fs {
		key := f.PortKey()
		was, is := forward.Closed(key), closed[key]
		if was == is {
			continue
		}
		diff = true
		if is != "" {
			log.Printf("port %s/%s closed: %s", f.Proto, f.Ports, is)
			events.Add(events.Event{Tunnel: f.Tunnel, Kind: events.Port, To: is, Detail: f.Ports, Auto: true})
		} else {
			log.Printf("port %s/%s open again", f.Proto, f.Ports)
			events.Add(events.Event{Tunnel: f.Tunnel, Kind: events.Port, From: was, To: "open", Detail: f.Ports, Auto: true})
		}
	}
	if diff {
		forward.SetClosed(closed)
		changed()
	}
}

// planClosed says why a sold port takes no traffic now ("" = it does).
func planClosed(p config.PortPlan, store *traffic.Store, name string, from int64, now time.Time) string {
	switch {
	case p.Paused:
		return forward.ClosedPaused
	case p.Expires > 0 && now.Unix() >= p.Expires:
		return forward.ClosedExpired
	case p.QuotaGB > 0:
		used, at := store.PeriodOf(name)
		if at == from && float64(used.Rx+used.Tx) >= p.QuotaGB*(1<<30) { // the panel's GB
			return forward.ClosedQuota
		}
	}
	return ""
}
