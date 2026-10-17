package cli

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	"neotunnel/internal/traffic"
	"neotunnel/internal/version"
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
	var keep, yes bool
	cmd := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"remove-all"},
		Short:   "Remove NeoTunnel completely: tunnels, service, rules, settings and the program",
		Long: "Stops the service, removes every tunnel, IPsec, forward and routing rule,\n" +
			"the kernel tuning, the configuration and keys, and the neotunnel program.\n" +
			"With --keep-config the configuration and keys stay for a later reinstall.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if !yes {
				what := "tunnels, service, settings, keys and the program"
				if keep {
					what = "tunnels, service and the program (settings kept)"
				}
				if !confirm(red("Remove NeoTunnel")+": "+what+"?", false) {
					info("nothing removed")
					return nil
				}
			}
			uninstall(keep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-config", false, "keep /etc/neotunnel (keys, tunnels, panel) for a later reinstall")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	// Older scripts call "uninstall --purge"; removing everything is now the default.
	cmd.Flags().Bool("purge", false, "")
	cmd.Flags().MarkHidden("purge")
	return cmd
}

func updateCmd() *cobra.Command {
	var ver string
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Install the latest release (or --version vX.Y.Z); tunnels and settings are kept",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			return selfUpdate(ver)
		},
	}
	cmd.Flags().StringVar(&ver, "version", "", "install this release instead of the latest, e.g. v0.2.0")
	return cmd
}

// selfUpdate runs the official installer, which replaces the program,
// restarts the service and keeps the configuration and panel password.
func selfUpdate(ver string) error {
	args := "--no-menu"
	if ver != "" {
		if !strings.HasPrefix(ver, "v") || strings.ContainsAny(ver, " ;&|$`'\"") {
			return fmt.Errorf("version looks like v0.2.0")
		}
		args += " --version " + ver
	}
	info("current version %s; downloading the installer…", version.Version)
	c := exec.Command("bash", "-c", "set -o pipefail; curl -fsSL "+version.InstallURL+" | bash -s -- "+args)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	return nil
}

// uninstall removes the service, every tunnel and rule, and the program;
// unless keep is set also the configuration, keys and kernel tuning.
func uninstall(keep bool) {
	service.Uninstall()
	engine.Down()
	success("service stopped; tunnels, IPsec and forwarding rules removed")
	if !keep {
		os.RemoveAll(config.Dir)
		os.Remove("/etc/sysctl.d/90-neotunnel.conf")
		os.Remove("/etc/modules-load.d/neotunnel.conf")
		success("configuration, keys and kernel tuning deleted")
	} else {
		info("configuration kept in %s", config.Dir)
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil && os.Remove(exe) == nil {
			success("program removed (%s)", exe)
		}
	}
	success("NeoTunnel is uninstalled")
}

func applyCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "apply ['NT1....']",
		Short: "Re-apply the configuration now, or join a main server with its setup key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if len(args) == 1 {
				return applyKey(args[0], force)
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			return report(engine.Apply(st))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "accept a setup key from a different main server")
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

func applyKey(key string, force bool) error {
	p, err := setuplink.Decode(key)
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
	cmd := &cobra.Command{
		Use:   "traffic",
		Short: "Live traffic and totals per tunnel (Ctrl+C to stop)",
		RunE:  func(cmd *cobra.Command, args []string) error { return showTraffic() },
	}
	cmd.AddCommand(trafficResetCmd())
	return cmd
}

// showTraffic redraws per-tunnel rates every second until Ctrl+C.
func showTraffic() error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	ts, _ := st.Effective()
	if len(ts) == 0 {
		info("no tunnels yet")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Println(dim("  ctrl+c to stop"))
	// Totals as of the daemon's last save (once a minute) plus what moved
	// since this command started.
	sums := traffic.Open().Summaries(time.Now())
	base := map[string]engine.LinkState{}
	prev := map[string]engine.LinkState{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for first := true; ; first = false {
		var b strings.Builder
		if !first && color {
			fmt.Fprintf(&b, "\x1b[%dA", len(ts)+1)
		}
		fmt.Fprintf(&b, "  %-12s %-6s %14s %14s %12s %12s\n", "TUNNEL", "STATE", "IN", "OUT", "TOTAL IN", "TOTAL OUT")
		for _, t := range ts {
			n := engine.Ifname(t.Name)
			cur := engine.TunnelState(t.Name)
			p, had := prev[n]
			in, out := "-", "-"
			if had && cur.Exists {
				in = bench.Rate(float64(cur.RxBytes-p.RxBytes) * 8)
				out = bench.Rate(float64(cur.TxBytes-p.TxBytes) * 8)
			}
			prev[n] = cur
			b0, ok := base[n]
			if !ok || cur.RxBytes < b0.RxBytes || cur.TxBytes < b0.TxBytes {
				base[n], b0 = cur, cur
			}
			moved := (cur.RxBytes - b0.RxBytes) + (cur.TxBytes - b0.TxBytes)
			s := sums[t.Name]
			state := green("up  ")
			if !cur.Up {
				state = red("down")
			}
			fmt.Fprintf(&b, "  %-12s %s   %14s %14s %12s %12s %12s\x1b[K\n", t.Name, state, in, out,
				bytesHuman(s.Today.Rx+s.Today.Tx+moved), bytesHuman(s.Month.Rx+s.Month.Tx+moved), bytesHuman(s.Total.Rx+s.Total.Tx+moved))
		}
		fmt.Print(b.String())
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
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
		RunE:  func(cmd *cobra.Command, args []string) error { return runTest(args[0], *o) },
	}
	o = benchOptions(cmd)
	return cmd
}

// runTest measures one tunnel and prints the result.
func runTest(name string, o bench.Options) error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := bench.TestTunnel(ctx, id, st, name, o, func(s string) { info("%s", s) })
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Print(bench.Table([]bench.Result{res}))
	return nil
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
			want := map[string]bool{}
			for _, s := range strings.Split(only, ",") {
				if s = strings.TrimSpace(s); s != "" {
					want[s] = true
				}
			}
			return runBench(args[0], *o, want)
		},
	}
	o = benchOptions(cmd)
	cmd.Flags().StringVar(&only, "only", "", "comma-separated subset, e.g. 6to4,ipip,vxlan,direct")
	return cmd
}

// runBench compares tunnel kinds to a peer (all, or those in only) and
// prints the ranking with the command that builds the fastest.
func runBench(peer string, o bench.Options, only map[string]bool) error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	p, ok := st.Peer(peer)
	if !ok {
		return fmt.Errorf("unknown server %q", peer)
	}
	if !p.Paired() {
		return fmt.Errorf("%s is not connected yet: run its connect command on it first", peer)
	}
	if _, err := netip.ParseAddr(st.Address); err != nil {
		return fmt.Errorf("this server's public IP is unknown: neotunnel node --address <ip>")
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	r := &bench.Runner{ID: id, State: st, Peer: *p, Opts: o, Progress: func(s string) { info("%s", s) }}
	title(fmt.Sprintf("Comparing tunnel kinds %s → %s", st.Self, p.Name))
	var results []bench.Result
	if len(only) == 0 || only["direct"] {
		results = append(results, r.Baseline(ctx))
	}
	var cands []bench.Candidate
	for _, c := range bench.Candidates(st.Self, p.Name, st.Address, p.Address) {
		if len(only) == 0 || only[c.Name] {
			cands = append(cands, c)
		}
	}
	results = append(results, r.Compare(ctx, cands, func(res bench.Result) {
		switch res.Status {
		case bench.StatusPassed:
			success("%s connects (%.1f ms)", res.Title, float64(res.Latency.Avg.Microseconds())/1000)
		case bench.StatusFailed:
			warn("%s: %s", res.Title, res.Err)
		case bench.StatusDone:
			info("%s: download %s, upload %s", res.Title, bench.Rate(res.Down), bench.Rate(res.Up))
		}
	})...)
	bench.Rank(results)
	title("Results (best first)")
	fmt.Print(bench.Table(results))
	for _, res := range results {
		if res.Err == "" && res.Mode != "" {
			fmt.Println()
			success("fastest tunnel: %s", bold(res.Title))
			cmd := fmt.Sprintf("neotunnel quick %s --mode %s", p.Name, res.Mode)
			if res.Port != 0 {
				cmd += fmt.Sprintf(" --port %d", res.Port)
			}
			info("set it up: %s", cmd)
			break
		}
	}
	return nil
}

func boostCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "boost",
		Short: "Apply the network tuning profile (BBR, fq, buffers) persistently",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if err := sysctl.ApplyBoost(); err != nil {
				return err
			}
			success("tuning applied and saved to /etc/sysctl.d/90-neotunnel.conf")
			return nil
		},
	}
}
