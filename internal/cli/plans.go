package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
)

// forwardSellCmd sets a sold port's plan: what its buyer gets.
func forwardSellCmd() *cobra.Command {
	var p config.PortPlan
	var days int
	var until string
	var restart, remove, resume bool
	cmd := &cobra.Command{
		Use:   "sell <tunnel> <ports>",
		Short: "Sell a port: speed cap, traffic quota, end date and client limits, enforced on this server",
		Example: `  neotunnel forward sell t1 443 --customer "Ali" --quota 100 --days 30 --rate 50 --ips 2
  neotunnel forward sell t1 443 --quota 200 --reset-days 30   # 200 GB every 30 days
  neotunnel forward sell t1 443 --pause                        # closed until --resume
  neotunnel forward sell t1 443 --remove                       # not sold any more`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if remove {
				ch, err := manage.SetPortPlan(args[0], args[1], nil, false)
				if err != nil {
					return err
				}
				printChange(ch)
				success("port %s is no longer sold: no limits on it", args[1])
				return nil
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			// Flags not given keep what the plan has.
			var cur config.PortPlan
			for _, f := range st.Forwards {
				if st.IsSelf(f.Node) && f.Tunnel == args[0] && f.Ports == args[1] && f.Plan != nil {
					cur = *f.Plan
				}
			}
			fl := cmd.Flags()
			keep := func(name string, dst *int, v int) {
				if !fl.Changed(name) {
					*dst = v
				}
			}
			if !fl.Changed("customer") {
				p.Customer = cur.Customer
			}
			keep("rate", &p.RateMbit, cur.RateMbit)
			keep("reset-days", &p.ResetDays, cur.ResetDays)
			keep("conns", &p.MaxConns, cur.MaxConns)
			keep("ips", &p.MaxIPs, cur.MaxIPs)
			if !fl.Changed("quota") {
				p.QuotaGB = cur.QuotaGB
			}
			p.Expires, p.Paused = cur.Expires, cur.Paused
			switch {
			case fl.Changed("days") && days == 0:
				p.Expires = 0
			case fl.Changed("days"):
				p.Expires = time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
			case until != "":
				t, err := time.ParseInLocation("2006-01-02", until, time.Local)
				if err != nil {
					return fmt.Errorf("--until takes a date like 2026-12-31")
				}
				p.Expires = t.AddDate(0, 0, 1).Unix() // through that day
			}
			if fl.Changed("pause") {
				p.Paused = true
			}
			if resume {
				p.Paused = false
			}
			ch, err := manage.SetPortPlan(args[0], args[1], &p, restart)
			if err != nil {
				return err
			}
			printChange(ch)
			success("port %s sold%s: %s", args[1], forWhom(p.Customer), planText(p))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&p.Customer, "customer", "", "whom the port is for (a note for you)")
	f.IntVar(&p.RateMbit, "rate", 0, "speed cap in Mbit/s each way, all of the port's clients together (0 = none)")
	f.Float64Var(&p.QuotaGB, "quota", 0, "traffic in GB, download + upload, then the port closes (0 = none)")
	f.IntVar(&p.ResetDays, "reset-days", 0, "the quota is whole again every so many days from the start (0 = one quota for the whole plan)")
	f.IntVar(&days, "days", 0, "the port closes this many days from now (0 = never)")
	f.StringVar(&until, "until", "", "or the last day it works, like 2026-12-31")
	f.IntVar(&p.MaxConns, "conns", 0, "open connections at once (0 = no limit)")
	f.IntVar(&p.MaxIPs, "ips", 0, "client IP addresses at once, each family on its own; an address counts until five minutes after its last packet (0 = no limit)")
	f.Bool("pause", false, "close the port until --resume")
	f.BoolVar(&resume, "resume", false, "open a paused port")
	f.BoolVar(&restart, "restart", false, "start the quota period over now")
	f.BoolVar(&remove, "remove", false, "stop selling the port: all limits off")
	return cmd
}

// forwardRenewCmd moves a sold port's end on.
func forwardRenewCmd() *cobra.Command {
	var days int
	var reset bool
	cmd := &cobra.Command{
		Use:   "renew <tunnel> <ports>",
		Short: "Renew a sold port: its end moves on; with --reset its quota is whole again from now",
		Example: `  neotunnel forward renew t1 443 --days 30           # the traffic used stays counted
  neotunnel forward renew t1 443 --days 30 --reset   # a new period: the quota whole again`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := manage.RenewPort(args[0], args[1], days, reset)
			if err != nil {
				return err
			}
			printChange(ch)
			if reset {
				success("port %s renewed, its quota whole again", args[1])
			} else {
				success("port %s renewed; the traffic used so far still counts", args[1])
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "days added to its end: from the end, or from now once it has passed (0 = end unchanged)")
	cmd.Flags().BoolVar(&reset, "reset", false, "start a new period now: the traffic used so far no longer counts against the quota")
	return cmd
}

func forWhom(customer string) string {
	if customer == "" {
		return ""
	}
	return " to " + customer
}

// planText sums a plan up in a line.
func planText(p config.PortPlan) string {
	var parts []string
	if p.RateMbit > 0 {
		parts = append(parts, fmt.Sprintf("≤%d Mbit/s", p.RateMbit))
	}
	if p.QuotaGB > 0 {
		q := fmt.Sprintf("%g GB", p.QuotaGB)
		if p.ResetDays > 0 {
			q += fmt.Sprintf(" every %d days", p.ResetDays)
		}
		parts = append(parts, q)
	}
	if p.Expires > 0 {
		parts = append(parts, "until "+time.Unix(p.Expires, 0).Format("2006-01-02 15:04"))
	}
	if p.MaxConns > 0 {
		parts = append(parts, fmt.Sprintf("%d connections", p.MaxConns))
	}
	if p.MaxIPs > 0 {
		parts = append(parts, fmt.Sprintf("%d IPs", p.MaxIPs))
	}
	if p.Paused {
		parts = append(parts, "paused")
	}
	if len(parts) == 0 {
		return "no limits"
	}
	return strings.Join(parts, ", ")
}
