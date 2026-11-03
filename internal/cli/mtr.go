package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/identity"
	"neotunnel/internal/manage"
	"neotunnel/internal/mtr"
)

func mtrCmd() *cobra.Command {
	var rounds int
	cmd := &cobra.Command{
		Use:   "mtr <tunnel>",
		Short: "Trace the path under a tunnel both ways, hop by hop: loss and ping at each router",
		Example: `  neotunnel mtr t1
  neotunnel mtr t1 --rounds 30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := identity.LoadOrCreate()
			if err != nil {
				return err
			}
			info("tracing for about %d seconds…", rounds+2)
			ctx, cancel := ctxTimeout(time.Duration(rounds)*time.Second + time.Minute)
			defer cancel()
			rep, err := manage.MTR(ctx, id, args[0], rounds)
			if err != nil {
				return err
			}
			printPath("This server → far end", rep.There)
			if rep.Back != nil {
				printPath("Far end → this server", *rep.Back)
			} else {
				warn("no path back: %s", rep.BackError)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&rounds, "rounds", 10, "probes per hop, a second apart (1-60)")
	return cmd
}

func printPath(name string, r mtr.Result) {
	title(name + " (" + r.Target + ")")
	fmt.Printf("  %3s  %-40s %6s %5s %7s %7s %7s %7s\n", "", "host", "loss", "sent", "last", "avg", "best", "worst")
	for _, h := range r.Hops {
		host := h.Host
		if host == "" {
			host = "???"
		}
		if h.IR {
			host += " (IR)"
		}
		fmt.Printf("  %3d. %-40s %5.1f%% %5d %7.1f %7.1f %7.1f %7.1f\n", h.TTL, host, h.Loss, h.Sent, h.Last, h.Avg, h.Best, h.Worst)
	}
	if !r.Reached {
		warn("the far end didn't answer within %d hops", mtr.MaxHops)
	}
}
