package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/guard"
	"neotunnel/internal/manage"
)

func guardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard [on|off|status]",
		Short: "The firewall: only what this server uses stays open (SSH and the panel always)",
		Args:  cobra.MaximumNArgs(1),
	}
	var iran bool
	var icmp, ifaces string
	cmd.Flags().BoolVar(&iran, "config-iran", false, "with on: forwards and Xray inbounds accept Iranian addresses only")
	cmd.Flags().StringVar(&icmp, "icmp", "", "with on: who gets an answer to ping: closed, iran or open")
	cmd.Flags().StringVar(&ifaces, "iface", "", "with on: interfaces to guard, comma-separated (default: the public ones)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := requireRoot(); err != nil {
			return err
		}
		st, err := loadNode()
		if err != nil {
			return err
		}
		g := config.Guard{}
		if st.Guard != nil {
			g = *st.Guard
		}
		what := "status"
		if len(args) == 1 {
			what = args[0]
		}
		switch what {
		case "on", "off":
			g.On = what == "on"
			if cmd.Flags().Changed("config-iran") {
				g.ConfigIran = iran
			}
			if icmp != "" {
				g.ICMP = icmp
			}
			if cmd.Flags().Changed("iface") {
				g.Ifaces = nil
				for _, n := range strings.Split(ifaces, ",") {
					if n = strings.TrimSpace(n); n != "" {
						g.Ifaces = append(g.Ifaces, n)
					}
				}
			}
			if _, err := manage.SetGuard(g, false); err != nil {
				return err
			}
			if st, err = config.Load(); err != nil {
				return err
			}
			if err := engine.ApplyGuard(st); err != nil {
				return err
			}
			if g.On {
				success("firewall on")
			} else {
				success("firewall off: nothing is filtered")
			}
		case "status":
		default:
			return fmt.Errorf("guard on, guard off or guard status")
		}
		_, s := guard.Build(st)
		printGuard(s)
		return nil
	}
	return cmd
}

func printGuard(s guard.Status) {
	if !s.On {
		info("firewall is off (turn it on: neotunnel guard on)")
		return
	}
	info("guarding %s; %d packet(s) dropped", strings.Join(s.Ifaces, ", "), s.Dropped)
	ssh := make([]string, len(s.SSH))
	for i, p := range s.SSH {
		ssh[i] = fmt.Sprint(p)
	}
	info("always open: SSH %s, panel %d, %d peer address(es)", strings.Join(ssh, ","), s.Panel, s.Peers)
	for _, p := range s.Ports {
		who := map[string]string{config.ScopeAll: "everyone", config.ScopeIran: "Iran only", config.ScopeClosed: "closed"}[p.Scope]
		what := p.Process
		if p.Forward {
			what = strings.TrimSpace("forward " + what)
		}
		info("%s/%d  %s  %s", p.Proto, p.Port, who, what)
	}
	src := "bundled"
	if s.Geo.Source == "downloaded" {
		src = "downloaded"
	}
	info("Iran address list: %d ranges (%s)", s.Geo.Ranges, src)
}
