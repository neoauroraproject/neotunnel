package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/fleet"
	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
)

func nodeCmd() *cobra.Command {
	var name, address string
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Show or set this node's name and public address",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name != "" || address != "" {
				if err := manage.SetNode(name, address); err != nil {
					return err
				}
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			id, err := identity.LoadOrCreate()
			if err != nil {
				return err
			}
			title("This node")
			fmt.Printf("  name     %s\n  address  %s\n  key      %s\n", bold(st.Self), st.Address, dim(identity.Fingerprint(id.PublicString())))
			if m := st.Managed; m != nil {
				state := green("paired")
				if m.EnrollToken != "" {
					state = yellow("waiting to reach master")
				}
				fmt.Printf("  master   %s (%s) %s, revision %d\n", bold(m.Master.Name), m.Master.Address, state, m.Revision)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "node name")
	cmd.Flags().StringVar(&address, "address", "", "public IP other servers reach this node at")
	return cmd
}

func peerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "peer", Short: "Manage the servers this node controls"}
	cmd.AddCommand(peerAddCmd(), peerListCmd(), peerRemoveCmd(), peerLinkCmd(), peerSyncCmd(), peerUpgradeCmd())
	return cmd
}

func peerAddCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "add <name> --address <ip>",
		Short: "Add a peer and print its setup command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := loadNode(); err != nil {
				return err
			}
			if err := manage.AddPeer(args[0], address); err != nil {
				return err
			}
			success("peer %s added", args[0])
			return showSetup(args[0])
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "peer's public IP (required)")
	cmd.MarkFlagRequired("address")
	return cmd
}

func peerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List peers and whether they are in sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			printPeers(st)
			return nil
		},
	}
}

func printPeers(st *config.State) {
	title("Peers")
	if len(st.Peers) == 0 {
		info("none yet — add one with: neotunnel peer add <name> --address <ip>")
		return
	}
	for _, p := range st.Peers {
		state := yellow("not paired")
		if p.Paired() {
			if p.AppliedRevision == st.Revision {
				state = green("in sync")
			} else {
				state = yellow(fmt.Sprintf("behind (rev %d of %d)", p.AppliedRevision, st.Revision))
			}
		}
		seen := ""
		if !p.LastSeen.IsZero() {
			seen = dim(" seen " + p.LastSeen.Local().Format("2006-01-02 15:04"))
		}
		fmt.Printf("  %-16s %-16s %s %s%s\n", bold(p.Name), p.Address, state, dim(p.Version), seen)
	}
}

func peerRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Forget a peer (its tunnels here are removed too)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := manage.RemovePeer(args[0])
			if err != nil {
				return err
			}
			printChange(ch)
			success("peer %s removed", args[0])
			return nil
		},
	}
}

func peerLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link <name>",
		Short: "Print the command that brings a peer to this node's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showSetup(args[0])
		},
	}
}

func peerSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [name...]",
		Short: "Push the current configuration to peers (all by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				for _, p := range st.Peers {
					args = append(args, p.Name)
				}
			}
			printChange(manage.Change{Peers: manage.Sync(st, args)})
			return nil
		},
	}
}

func peerUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade [name...]",
		Short: "Install this node's neotunnel version on peers (all by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			id, err := identity.LoadOrCreate()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				for _, p := range st.Peers {
					if p.Paired() {
						args = append(args, p.Name)
					}
				}
			}
			for _, p := range args {
				ctx, cancel := ctxTimeout(2 * time.Minute)
				v, err := fleet.Upgrade(ctx, id, st, p)
				cancel()
				if err != nil {
					bad("%s: %v", p, err)
					continue
				}
				success("%s now runs %s", p, v)
			}
			return nil
		},
	}
}
