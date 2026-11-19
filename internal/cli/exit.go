package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/manage"
	"neotunnel/internal/neoexit"
)

// NeoExit (free exits for AI services) on this server and the ones it
// manages; everything else about it is on the panel's NeoExit page.

func exitCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "exit", Short: "NeoExit (free exits for AI services) on this server and its peers: status, install, remove"}
	cmd.AddCommand(exitStatusCmd(), exitInstallCmd(), exitRemoveCmd())
	return cmd
}

// exitServer turns "" into this server.
func exitServer(args []string) string {
	if len(args) == 0 || args[0] == "" || args[0] == "self" {
		return manage.ExitSelf
	}
	return args[0]
}

func exitStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show NeoExit on this server and each peer",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			xs, err := manage.ExitServersNow(ctx)
			if err != nil {
				return err
			}
			printExits(xs)
			return nil
		},
	}
}

func printExits(xs []manage.ExitServer) {
	title("NeoExit")
	for _, x := range xs {
		state := dim("not installed")
		switch {
		case !x.Reachable:
			state = yellow("not reachable")
		case x.Old:
			state = yellow("NeoTunnel there is too old (upgrade it)")
		case x.State == neoexit.StateRunning && x.Status != nil:
			bad := 0
			for _, s := range x.Status.Services {
				if s.State != "ok" {
					bad++
				}
			}
			state = green("running " + x.Status.Version)
			if bad > 0 {
				state += yellow(fmt.Sprintf(" · %d services blocked", bad))
			}
		case x.State == neoexit.StateInstalled:
			state = red("installed, not running")
		}
		name := x.Name
		if x.Key == manage.ExitSelf {
			name += dim(" (this server)")
		}
		fmt.Printf("  %-28s %s\n", bold(name), state)
		if x.Status != nil {
			for _, s := range x.Status.Services {
				fmt.Printf("      %-12s %-10s %s\n", s.Name, s.State, dim(s.Exit))
			}
		}
	}
	info("manage exits, services and the 3x-ui setup on the panel's NeoExit page")
}

func exitInstallCmd() *cobra.Command {
	var ver string
	cmd := &cobra.Command{
		Use:   "install [server]",
		Short: "Install or upgrade NeoExit on this server or on a peer (settings are kept)",
		Long: "Installs NeoExit, or upgrades it keeping its settings, on this server (no\n" +
			"argument) or on a peer. A server that can't reach GitHub gets the files\n" +
			"from this server, which downloads them itself or through another peer.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			server := exitServer(args)
			info("downloading and setting up NeoExit; this can take a few minutes")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			res, err := manage.ExitInstall(ctx, server, ver)
			if err != nil {
				return err
			}
			success("NeoExit %s installed", res.Version)
			fmt.Printf("  panel     https://<server-ip>:%d/%s/\n  username  %s\n", res.PanelPort, res.PanelPath, res.User)
			if res.Password != "" {
				fmt.Printf("  password  %s   %s\n", res.Password, dim("(shown only now)"))
			}
			fmt.Printf("  proxy     %s (SOCKS5 and HTTP)\n", res.Proxy)
			return nil
		},
	}
	cmd.Flags().StringVar(&ver, "version", "latest", "release to install (vX.Y.Z)")
	return cmd
}

func exitRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove [server]",
		Short: "Remove NeoExit and its settings from this server or a peer",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			server := exitServer(args)
			if !yes && !confirm(red("Remove NeoExit")+" and its settings from "+bold(map[bool]string{true: "this server", false: server}[server == manage.ExitSelf])+"?", false) {
				info("nothing removed")
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := manage.ExitUninstall(ctx, server); err != nil {
				return err
			}
			success("NeoExit removed")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}
