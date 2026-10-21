// Package cli is the neotunnel command line and interactive menu.
package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
	"neotunnel/internal/version"
)

func Execute() {
	if err := cobraRoot().Execute(); err != nil {
		bad("%v", err)
		os.Exit(1)
	}
}

func cobraRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "neotunnel",
		Short:         "Kernel tunnels (6to4, IPIP, GRE, VXLAN, ...) between servers, managed from one place",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return menu()
		},
	}
	root.AddCommand(
		versionCmd(), daemonCmd(), installCmd(), updateCmd(), uninstallCmd(), nodeCmd(), panelCmd(),
		peerCmd(), tunnelCmd(), forwardCmd(), quickCmd(),
		applyCmd(), downCmd(), statusCmd(), trafficCmd(), testCmd(), benchCmd(), boostCmd(), mtuCmd(), doctorCmd(), guardCmd(),
	)
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(*cobra.Command, []string) { fmt.Println(version.Version) },
	}
}

func loadNode() (*config.State, error) { return manage.Node() }

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// printChange reports a change: local problems, then each peer's outcome,
// with the command to run on any peer that couldn't be reached.
func printChange(ch manage.Change) {
	for _, e := range ch.Errors {
		warn("%s", e)
	}
	for _, p := range ch.Peers {
		if p.OK {
			success("%s updated (revision %d)", p.Peer, p.Revision)
			for _, w := range p.Warnings {
				warn("%s: %s", p.Peer, w)
			}
			continue
		}
		warn("%s is not reachable (%s)", p.Peer, p.Error)
		if p.Setup != nil {
			printSetup(p.Peer, *p.Setup)
		}
	}
}

// printSetup shows the commands that bring a peer to the current state.
func printSetup(peer string, s fleet.Setup) {
	where := bold(peer) + " (" + s.Address + ")"
	if s.Install != "" {
		fmt.Println("  Run " + bold("one") + " of these on " + where + " as root:")
		fmt.Println()
		fmt.Println(accent("  1) ") + "install from this server and connect " + dim("(works without GitHub access)"))
		fmt.Println()
		fmt.Println(s.Install)
		fmt.Println()
		fmt.Println(accent("  2) ") + "or install from GitHub and connect")
		fmt.Println()
		fmt.Println(s.GitHub)
		fmt.Println()
		fmt.Println(dim("  NeoTunnel already installed there? Only the last part is needed:"))
	} else {
		fmt.Println("  " + where + " is connected; changes reach it automatically.")
		fmt.Println("  If it was offline, run this on it to apply everything at once:")
	}
	fmt.Println()
	fmt.Println(s.Apply)
	fmt.Println()
}

func showSetup(peer string) error {
	s, err := manage.Setup(peer)
	if err != nil {
		return err
	}
	printSetup(peer, s)
	return nil
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as root (sudo neotunnel ...)")
	}
	return nil
}
