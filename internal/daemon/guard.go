package daemon

import (
	"context"
	"log"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/guard"
)

// guardEvery is how soon a port a program starts listening on (a new 3x-ui
// inbound) is opened, or a port it stopped using is closed.
const guardEvery = 5 * time.Second

// watchGuard keeps the firewall in line with what the server listens on,
// turns it off again when the panel did not confirm in time that it could
// still reach the server, and updates the Iran list weekly.
func watchGuard(ctx context.Context) {
	t := time.NewTicker(guardEvery)
	defer t.Stop()
	var lastErr string
	var lastGeo time.Time
	for {
		if st, err := config.Load(); err == nil && st.Guard != nil {
			g := st.Guard
			if g.On && g.ConfirmBy != 0 && time.Now().Unix() > g.ConfirmBy {
				st, _ = config.Update(func(s *config.State) error {
					if s.Guard != nil {
						s.Guard.On, s.Guard.ConfirmBy = false, 0
					}
					return nil
				})
				log.Printf("firewall turned off again: the panel did not confirm it could still reach this server")
			}
			if st != nil {
				err := engine.ApplyGuard(st)
				if msg := errText(err); msg != lastErr {
					if msg != "" {
						log.Printf("firewall: %s", msg)
					}
					lastErr = msg
				}
				if st.Guard != nil && st.Guard.On && guard.NeedsUpdate() && time.Since(lastGeo) > time.Hour {
					lastGeo = time.Now()
					go func() {
						if info, err := guard.UpdateIR(ctx); err == nil {
							log.Printf("firewall: Iran address list updated (%d ranges)", info.Ranges)
						}
					}()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
