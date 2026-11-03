package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
)

func historyCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List the changes to tunnels and ports, newest first, to roll one back",
		RunE: func(cmd *cobra.Command, args []string) error {
			h := config.History()
			title("Changes")
			if len(h) == 0 {
				info("none yet")
			}
			for i, s := range h {
				if i == limit {
					info("… %d older (--limit)", len(h)-limit)
					break
				}
				what := diffText(s.Changes)
				if s.Rollback != "" {
					what = "rollback: " + what
				}
				fmt.Printf("  %s  %s  rev %d  %s\n", s.ID, time.Unix(s.At, 0).Format("2006-01-02 15:04"), s.Revision, what)
			}
			if len(h) > 0 {
				fmt.Println()
				info("put it back as before a change: neotunnel rollback <id>")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "how many to list")
	return cmd
}

func rollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback <id>",
		Short: "Put tunnels and ports back as they were before a change (see: neotunnel history), here and on the peers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := manage.Rollback(args[0])
			if err != nil {
				return err
			}
			printChange(ch)
			success("rolled back; this too is in the history and can be undone")
			return nil
		},
	}
}

// diffText sums a change up: "tunnel t3 added, port tcp/443 changed".
func diffText(ds []config.Diff) string {
	var parts []string
	for _, d := range ds {
		if d.Name == "" {
			parts = append(parts, d.Kind+" "+d.Op)
		} else {
			parts = append(parts, d.Kind+" "+d.Name+" "+d.Op)
		}
	}
	return strings.Join(parts, ", ")
}
