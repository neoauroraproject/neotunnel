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
	"neotunnel/internal/fleet"
	"neotunnel/internal/forward"
	"neotunnel/internal/health"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/metrics"
	"neotunnel/internal/probe"
	"neotunnel/internal/safe"
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
	// The agent's own port, picked once at random (the main server learns
	// it from the node's status), and 7710 on nodes paired there before.
	st, err := config.Update(func(s *config.State) error {
		if s.AgentPort != 0 && !config.TCPPortFree(s.AgentPort) {
			// Another program took it (it was picked without a check, or
			// while the service was down): pick another, keeping 7710 too
			// so the main server can still reach this node and learn it.
			log.Printf("agent port %d is in use by another program; picking a new one", s.AgentPort)
			s.AgentPort, s.AgentLegacy = 0, true
		}
		legacy := s.AgentLegacy
		config.EnsureAgentPort(s, config.TCPPortFree)
		s.AgentLegacy = s.AgentLegacy || legacy
		return nil
	})
	if err != nil {
		return err
	}
	for i, port := range st.AgentPorts() {
		go func(port int, main bool) {
			if err := srv.ListenAndServe(":" + strconv.Itoa(port)); err != nil {
				log.Printf("agent server on port %d: %v", port, err)
				if main {
					os.Exit(1)
				}
			}
		}(port, i == 0)
	}

	dp := newDataPlane()
	engine.SetUserspace(dp)
	defer dp.Close()

	tracker := health.New()
	go monitorHealth(ctx, tracker, poke)

	store := traffic.Open()
	history := metrics.Open()
	go account(ctx, store, history, tracker, poke)
	go probeMTUs(ctx, poke)

	hooks := safeHooks(id)
	go watchSafe(ctx, hooks, poke)

	startPanel(id, store, history, tracker, hooks, poke)
	go watchPeers(ctx, id)

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
			stop(store)
			return nil
		case <-term:
			stop(store)
			return nil
		case <-t.C:
		case <-hup:
		case <-kick:
		}
	}
}

// stop is the daemon's last work: the traffic totals are saved, and the
// downloads redirect goes with the listener it sends connections to.
func stop(store *traffic.Store) {
	store.Save()
	forward.StopFetch()
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
func startPanel(id *identity.Identity, store *traffic.Store, history *metrics.Store, tracker *health.Tracker, hooks safe.Hooks, reconcile func()) {
	st, err := config.Load()
	if err != nil || st.Panel == nil {
		return
	}
	srv := &web.Server{
		Backend: &web.Live{ID: id, Started: time.Now(), Problems: currentProblems, Store: store, History: history, Health: tracker, Reconcile: reconcile, Safe: hooks},
		Path:    st.Panel.Path,
		Restart: func() {
			log.Printf("restarting for the panel (new port or a restored backup)")
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

// fixClashes moves our tunnels off what a peer reports taken there by
// another main server's tunnels (see manage.FixClashes).
func fixClashes() {
	st, err := config.Load()
	if err != nil {
		return
	}
	for _, p := range st.Peers {
		if !p.Paired() || (len(p.Busy) == 0 && len(p.BusyPorts) == 0) {
			continue
		}
		moved, ch, err := manage.FixClashes(p.Name)
		if err != nil {
			log.Printf("tunnels to %s: %v", p.Name, err)
			continue
		}
		if len(moved) > 0 {
			log.Printf("%s uses their addresses or ports for another main server's tunnels; moved %v (%d error(s))", p.Name, moved, len(ch.Errors))
		}
	}
}

// watchPeers keeps each peer's version and last contact current, so the
// panel can tell which servers are reachable and which need an upgrade.
func watchPeers(ctx context.Context, id *identity.Identity) {
	every := time.Minute
	// Tests shorten it (NEOTUNNEL_PEER_WATCH=3s) to see a resync quickly.
	if d, err := time.ParseDuration(os.Getenv("NEOTUNNEL_PEER_WATCH")); err == nil && d > 0 {
		every = d
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		fleet.RefreshAll(ctx, id)
		fixClashes()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tryRegister completes pairing for a node that received its setup key
// while the master was unreachable.
func tryRegister(ctx context.Context, id *identity.Identity, st *config.State) {
	for _, m := range st.Masters() {
		if m.EnrollToken != "" {
			register(ctx, id, m)
		}
	}
}

func register(ctx context.Context, id *identity.Identity, m *config.Managed) {
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
		if m := s.MasterByKey(m.Master.PublicKey); m != nil {
			m.EnrollToken = ""
		}
		return nil
	})
	log.Printf("paired with master %s", m.Master.Name)
}
