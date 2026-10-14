package cli

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/agent"
	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/daemon"
	"neotunnel/internal/engine"
	"neotunnel/internal/identity"
	"neotunnel/internal/service"
	"neotunnel/internal/setuplink"
	"neotunnel/internal/sysctl"
)

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "daemon",
		Short:  "Run the background service (systemd runs this)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return daemon.Run(ctx)
		},
	}
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the systemd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if _, err := loadNode(); err != nil {
				return err
			}
			if err := service.Install(); err != nil {
				return err
			}
			success("service running; tunnels come back automatically after reboot")
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the service and remove all tunnels",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			service.Uninstall()
			engine.Down()
			if purge {
				os.RemoveAll(config.Dir)
				os.Remove("/etc/sysctl.d/90-neotunnel.conf")
			}
			success("uninstalled")
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete configuration and keys")
	return cmd
}

func applyCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "apply ['neotunnel://...']",
		Short: "Apply the configuration now, or join/catch up with a master's setup link",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if len(args) == 1 {
				return applyLink(args[0], force)
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			return report(engine.Apply(st))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "accept a link from a different master")
	return cmd
}

func report(res engine.Result) error {
	for _, n := range res.Created {
		success("%s up", n)
	}
	for _, n := range res.Removed {
		info("%s removed", n)
	}
	for _, e := range res.Errors {
		bad("%v", e)
	}
	if len(res.Errors) == 0 {
		success("%d tunnel(s) in place", len(res.Specs))
	}
	return nil
}

func applyLink(link string, force bool) error {
	p, err := setuplink.Decode(link)
	if err != nil {
		return err
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	st, err := config.Update(func(s *config.State) error { return setuplink.Accept(s, p, force) })
	if err != nil {
		return err
	}
	success("configuration from %s accepted (revision %d, %d tunnel(s))", p.Master.Name, p.Revision, len(p.Tunnels))
	report(engine.Apply(st))

	if st.Managed.EnrollToken != "" {
		ctx, cancel := ctxTimeout(10 * time.Second)
		err := agent.Register(ctx, id, st.Managed)
		cancel()
		if err == nil {
			config.Update(func(s *config.State) error { s.Managed.EnrollToken = ""; return nil })
			success("paired with %s — future changes arrive automatically", p.Master.Name)
		} else {
			warn("could not reach %s yet (%v); the service keeps trying", p.Master.Name, err)
		}
	}
	if !service.Installed() {
		if err := service.Install(); err != nil {
			warn("could not install the systemd service (%v); run 'neotunnel daemon' yourself", err)
			return nil
		}
		success("service installed")
	} else {
		service.Reload()
	}
	return nil
}

func downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Remove all tunnel interfaces now (the service recreates them unless stopped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if err := engine.Down(); err != nil {
				return err
			}
			success("all neotunnel interfaces removed")
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this node, its tunnels and peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			printStatus(st)
			return nil
		},
	}
}

func printStatus(st *config.State) {
	svc := red("stopped")
	if exec.Command("systemctl", "is-active", "--quiet", service.Name).Run() == nil {
		svc = green("running")
	}
	title("NeoTunnel")
	fmt.Printf("  node %s  %s  service %s\n", bold(st.Self), dim(st.Address), svc)
	ts, _ := st.Effective()
	title("Tunnels")
	if len(ts) == 0 {
		info("none yet — the fastest start: neotunnel quick <peer> --address <ip>")
	}
	for _, t := range ts {
		printTunnel(st, t)
	}
	if len(st.Peers) > 0 {
		printPeers(st)
	}
}

func trafficCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "traffic",
		Short: "Live traffic per tunnel (Ctrl+C to stop)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			ts, _ := st.Effective()
			if len(ts) == 0 {
				info("no tunnels")
				return nil
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			prev := map[string]engine.LinkState{}
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for first := true; ; first = false {
				var b strings.Builder
				if !first && color {
					fmt.Fprintf(&b, "\x1b[%dA", len(ts)+1)
				}
				fmt.Fprintf(&b, "%-12s %-8s %14s %14s %12s %12s\n", "TUNNEL", "STATE", "IN", "OUT", "TOTAL IN", "TOTAL OUT")
				for _, t := range ts {
					n := engine.Ifname(t.Name)
					cur := engine.State(n)
					p, had := prev[n]
					in, out := "-", "-"
					if had && cur.Exists {
						in = bench.Rate(float64(cur.RxBytes-p.RxBytes) * 8)
						out = bench.Rate(float64(cur.TxBytes-p.TxBytes) * 8)
					}
					prev[n] = cur
					state := "up"
					if !cur.Up {
						state = "down"
					}
					fmt.Fprintf(&b, "%-12s %-8s %14s %14s %12s %12s\x1b[K\n", t.Name, state, in, out, bytesHuman(cur.RxBytes), bytesHuman(cur.TxBytes))
				}
				fmt.Print(b.String())
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
				}
			}
		},
	}
}

func benchOptions(cmd *cobra.Command) *bench.Options {
	o := bench.DefaultOptions()
	f := cmd.Flags()
	f.DurationVar(&o.Duration, "duration", o.Duration, "throughput test length per direction")
	f.IntVar(&o.Streams, "streams", o.Streams, "parallel TCP streams")
	f.IntVar(&o.Pings, "pings", o.Pings, "latency probes")
	return &o
}

func testCmd() *cobra.Command {
	var o *bench.Options
	cmd := &cobra.Command{
		Use:   "test <tunnel>",
		Short: "Measure ping, jitter, loss and bandwidth through a tunnel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			id, err := identity.LoadOrCreate()
			if err != nil {
				return err
			}
			title("Testing " + args[0])
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			res, err := bench.TestTunnel(ctx, id, st, args[0], *o, func(s string) { info("%s", s) })
			if err != nil {
				return err
			}
			fmt.Println()
			fmt.Print(bench.Table([]bench.Result{res}))
			return nil
		},
	}
	o = benchOptions(cmd)
	return cmd
}

func benchCmd() *cobra.Command {
	var o *bench.Options
	var only string
	cmd := &cobra.Command{
		Use:   "bench <peer>",
		Short: "Try every tunnel kind to a peer and rank them by speed",
		Long: "Brings up temporary tunnels of each kind on both servers, measures them,\n" +
			"removes them and recommends the fastest. Existing tunnels are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			p, ok := st.Peer(args[0])
			if !ok {
				return fmt.Errorf("unknown peer %q", args[0])
			}
			if _, err := netip.ParseAddr(st.Address); err != nil {
				return fmt.Errorf("this node's address is unknown: neotunnel node --address <ip>")
			}
			id, err := identity.LoadOrCreate()
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, s := range strings.Split(only, ",") {
				if s = strings.TrimSpace(s); s != "" {
					want[s] = true
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			r := &bench.Runner{ID: id, State: st, Peer: *p, Opts: *o, Progress: func(s string) { info("%s", s) }}
			title(fmt.Sprintf("Benchmark %s → %s", st.Self, p.Name))
			var results []bench.Result
			if len(want) == 0 || want["direct"] {
				results = append(results, r.Baseline(ctx))
			}
			for _, c := range bench.Candidates(st.Self, p.Name, st.Address, p.Address) {
				if len(want) > 0 && !want[c.Name] {
					continue
				}
				if ctx.Err() != nil {
					break
				}
				res := r.Run(ctx, c)
				if res.Err != "" {
					warn("%s: %s", c.Title, res.Err)
				}
				results = append(results, res)
			}
			bench.Rank(results)
			title("Results (best first)")
			fmt.Print(bench.Table(results))
			for _, res := range results {
				if res.Err == "" && res.Name != "direct" {
					fmt.Println()
					success("fastest tunnel: %s", bold(res.Title))
					info("set it up: neotunnel quick %s --mode %s", p.Name, res.Name)
					break
				}
			}
			return nil
		},
	}
	o = benchOptions(cmd)
	cmd.Flags().StringVar(&only, "only", "", "comma-separated subset, e.g. 6to4,ipip,vxlan,direct")
	return cmd
}

func optimizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "optimize",
		Short: "Apply the network tuning profile (BBR, fq, buffers) persistently",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if err := sysctl.ApplyOptimize(); err != nil {
				return err
			}
			success("tuning applied and saved to /etc/sysctl.d/90-neotunnel.conf")
			return nil
		},
	}
}
