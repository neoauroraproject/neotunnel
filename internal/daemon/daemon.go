// Package daemon is the long-running service: it keeps the kernel in sync
// with the state file, serves the agent and probe ports, and completes
// pairing with the master when a setup key was applied offline.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/probe"
	"neotunnel/internal/traffic"
	"neotunnel/internal/web"
)

const reconcileEvery = 15 * time.Second

func Run(ctx context.Context) error {
	id, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	kick := make(chan struct{}, 1)
	poke := func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}

	ps := probe.NewServer()
	go func() {
		if err := ps.Serve(":" + strconv.Itoa(probe.DefaultPort)); err != nil {
			log.Printf("probe server: %v", err)
		}
	}()
	srv := &agent.Server{ID: id, Probe: ps, Started: time.Now(), Applied: poke}
	go func() {
		addr := ":" + strconv.Itoa(agent.DefaultPort)
		if err := srv.ListenAndServe(addr); err != nil {
			log.Printf("agent server: %v", err)
			os.Exit(1)
		}
	}()

	dp := newDataPlane()
	engine.SetUserspace(dp)
	defer dp.Close()

	store := traffic.Open()
	go account(ctx, store)
	go probeMTUs(ctx, poke)

	startPanel(id, store, poke)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	log.Printf("neotunnel daemon started, key %s", identity.Fingerprint(id.PublicString()))

	t := time.NewTicker(reconcileEvery)
	defer t.Stop()
	lastErr := ""
	for {
		st, err := config.Load()
		if err != nil {
			log.Printf("load state: %v", err)
		} else {
			res := engine.Apply(st)
			for _, n := range res.Created {
				log.Printf("tunnel up: %s", n)
			}
			for _, n := range res.Removed {
				log.Printf("tunnel removed: %s", n)
			}
			setProblems(res.Errors)
			// Log persistent errors once, not every tick.
			if e := fmt.Sprint(res.Err()); res.Err() != nil && e != lastErr {
				log.Printf("reconcile: %s", e)
				lastErr = e
			} else if res.Err() == nil {
				lastErr = ""
			}
			tryRegister(ctx, id, st)
		}
		select {
		case <-ctx.Done():
			store.Save()
			return nil
		case <-term:
			store.Save()
			return nil
		case <-t.C:
		case <-hup:
		case <-kick:
		}
	}
}

var (
	lastRegisterErr string

	problemsMu sync.Mutex
	problems   []string
)

func setProblems(errs []error) {
	problemsMu.Lock()
	defer problemsMu.Unlock()
	problems = problems[:0]
	for _, e := range errs {
		problems = append(problems, e.Error())
	}
}

func currentProblems() []string {
	problemsMu.Lock()
	defer problemsMu.Unlock()
	return append([]string(nil), problems...)
}

// startPanel serves the web panel when it is configured.
func startPanel(id *identity.Identity, store *traffic.Store, reconcile func()) {
	st, err := config.Load()
	if err != nil || st.Panel == nil {
		return
	}
	srv := &web.Server{
		Backend: &web.Live{ID: id, Started: time.Now(), Problems: currentProblems, Store: store, Reconcile: reconcile},
		Path:    st.Panel.Path,
		Restart: func() {
			log.Printf("panel port changed; restarting")
			os.Exit(0) // systemd (Restart=always) brings us back on the new port
		},
	}
	go func() {
		log.Printf("panel listening on port %d", st.Panel.Port)
		if err := srv.ListenAndServe(st.Panel.Port, st.Address); err != nil {
			log.Printf("panel: %v", err)
		}
	}()
}

// tryRegister completes pairing for a node that received its setup key
// while the master was unreachable.
func tryRegister(ctx context.Context, id *identity.Identity, st *config.State) {
	m := st.Managed
	if m == nil || m.EnrollToken == "" {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := agent.Register(rctx, id, m); err != nil {
		var ne net.Error
		if !errors.As(err, &ne) && err.Error() != lastRegisterErr {
			log.Printf("register with master %s: %v", m.Master.Name, err)
			lastRegisterErr = err.Error()
		}
		return // retry next tick
	}
	config.Update(func(s *config.State) error {
		if s.Managed != nil {
			s.Managed.EnrollToken = ""
		}
		return nil
	})
	log.Printf("paired with master %s", m.Master.Name)
}
