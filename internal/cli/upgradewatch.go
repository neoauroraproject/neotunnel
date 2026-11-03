package cli

import (
	"github.com/spf13/cobra"

	"neotunnel/internal/upgrade"
)

// upgradeWatchCmd is the watchdog an upgrade starts from the old binary
// (see package upgrade); not for people to run.
func upgradeWatchCmd() *cobra.Command {
	var want, exe string
	cmd := &cobra.Command{
		Use:    "upgrade-watch",
		Short:  "Undo an upgrade that doesn't answer its main server in time",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return upgrade.RunWatch(want, exe, upgrade.Deadline)
		},
	}
	cmd.Flags().StringVar(&want, "version", "", "the new version")
	cmd.Flags().StringVar(&exe, "exe", "", "where the installed binary is")
	cmd.MarkFlagRequired("version")
	cmd.MarkFlagRequired("exe")
	return cmd
}
