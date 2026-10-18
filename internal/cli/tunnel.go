package cli

import (
	"fmt"
	"net"
	"strings"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/failover"
	"neotunnel/internal/manage"
	"neotunnel/internal/mikrotik"
)

func tunnelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tunnel", Short: "Add, list and remove tunnels"}
	cmd.AddCommand(tunnelAddCmd(), tunnelSetCmd(), tunnelLimitCmd(), tunnelPowerCmd(true), tunnelPowerCmd(false), tunnelListCmd(), tunnelRemoveCmd(), tunnelMikroTikCmd(), kindsCmd())
	return cmd
}

func kindsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kinds",
		Short: "List supported tunnel kinds",
		Run: func(*cobra.Command, []string) {
			title("Tunnel kinds")
			for _, k := range config.Kinds() {
				under := "IPv4"
				if k.Under6 {
					under = "IPv6"
				} else if k.UnderAny {
					under = "IPv4/6"
				}
				port := ""
				if k.CanUsePort() {
					port = "port"
				}
				fmt.Printf("  %-10s L%d  over %-6s %-5s %s\n", bold(string(k.Kind)), k.Layer, under, port, dim(k.Title))
			}
			fmt.Println()
			info("stacks: 'neotunnel quick' builds 6to4 (sit + ipip6 or gre6) in one step")
		},
	}
}

func tunnelAddCmd() *cobra.Command {
	var o manage.TunnelOpts
	cmd := &cobra.Command{
		Use:   "add <name> --kind <kind> (--peer <peer> | --remote <ip>)",
		Short: "Add a tunnel to a managed peer or to any remote endpoint (e.g. a MikroTik)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			withDefaults(cmd.Flags(), st, &o.Settings, nil)
			t, err := manage.NewTunnel(st, args[0], o)
			if err != nil {
				return err
			}
			saved, ch, err := manage.AddTunnel(t)
			if err != nil {
				return err
			}
			printChange(ch)
			st, _ = loadNode()
			printTunnel(st, saved)
			if saved.Kind == config.KindWireGuard && o.Remote != "" {
				pub, _ := engine.WireGuardPublicKey(saved, 0)
				info("this server's WireGuard public key: %s (port %d)", bold(pub), saved.Port)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Kind, "kind", "", "tunnel kind (see: neotunnel tunnel kinds)")
	f.StringVar(&o.Peer, "peer", "", "managed peer for the other end")
	f.StringVar(&o.Remote, "remote", "", "remote endpoint IP when the far end is not managed by neotunnel")
	f.StringVar(&o.Over, "over", "", "run on top of another tunnel")
	f.Uint16Var(&o.Port, "port", 0, "UDP port the tunnel runs on (FOU for sit/ipip/gre/gretap; vxlan/geneve/wireguard/obfs port; L2TPv3 and EoIP over UDP)")
	settingsFlags(f, &o.Settings)
	f.Uint32Var(&o.VNI, "vni", 0, "VNI for vxlan/geneve (default: derived from name)")
	cmd.MarkFlagRequired("kind")
	return cmd
}

func tunnelListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tunnels with their addresses",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			ts, _ := st.Effective()
			title("Tunnels")
			if len(ts) == 0 {
				info("none yet — the fastest start: neotunnel quick <peer> --address <ip>")
			}
			for _, t := range ts {
				printTunnel(st, t)
			}
			return nil
		},
	}
}

func printTunnel(st *config.State, t config.Tunnel) {
	me := 0
	if st.IsSelf(t.Sides[1].Node) {
		me = 1
	}
	far := t.Sides[1-me].Node
	if far == "" {
		far = t.Sides[1-me].Endpoint
	}
	ls := engine.TunnelState(t.Name)
	extra := ""
	if t.Over != "" {
		extra += dim(" over " + t.Over)
	}
	if t.Port != 0 {
		extra += dim(fmt.Sprintf(" udp/%d", t.Port))
	}
	if n := t.LaneCount(); n > 1 {
		extra += dim(fmt.Sprintf(" %d lanes", n))
	}
	if t.Disabled {
		extra += dim(" (disabled)")
	}
	if t.RateMbit > 0 {
		extra += dim(fmt.Sprintf(" ≤%d Mbit/s", t.RateMbit))
	}
	if t.QuotaGB > 0 {
		extra += dim(fmt.Sprintf(" quota %g GB/month", t.QuotaGB))
	}
	if st.Managed != nil {
		if _, local := st.Tunnel(t.Name); !local {
			extra += dim(" (managed by " + st.Managed.Master.Name + ")")
		}
	}
	fmt.Printf("  %s %-10s %-8s → %s%s\n", onOff(ls.Up), bold(t.Name), t.Kind, far, extra)
	for _, n := range []string{t.Net4, t.Net6} {
		if n == "" {
			continue
		}
		l, _ := config.Inner(n, me)
		r, _ := config.Inner(n, 1-me)
		fmt.Printf("      %s %-26s %s %s\n", dim("here"), l, dim("there"), r.Addr())
	}
	if ls.Exists {
		fmt.Printf("      %s %d  %s %s  %s %s\n", dim("mtu"), ls.MTU, dim("rx"), bytesHuman(ls.RxBytes), dim("tx"), bytesHuman(ls.TxBytes))
	}
}

func tunnelMikroTikCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "mikrotik <name>",
		Aliases: []string{"routeros"},
		Short:   "Print the RouterOS v7 commands for a MikroTik at the tunnel's far end",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			out, err := mikrotik.Commands(st, args[0])
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

func tunnelRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a tunnel on both ends (with tunnels and forwards on top of it)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			drop, ch, err := manage.RemoveTunnel(args[0])
			if err != nil {
				return err
			}
			printChange(ch)
			success("removed %s", strings.Join(drop, ", "))
			return nil
		},
	}
}

// proxyAddr is where a socks forward listens.
func proxyAddr(listen, port string) string {
	if listen == "" {
		listen = "127.0.0.1"
	}
	return net.JoinHostPort(listen, port)
}

func forwardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "forward", Short: "Forward public ports on this node through a tunnel"}
	var proto string
	var to uint16
	var mode manage.ForwardRequest
	add := &cobra.Command{
		Use:   "add <tunnel> <ports>",
		Short: "Forward ports (e.g. 443,8080-8090) to the tunnel's far end",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (mode.Mode == config.ForwardRelay || mode.Mode == config.ForwardSocks) && !cmd.Flags().Changed("proto") {
				proto = "tcp" // relay mode and proxies carry TCP only
			}
			ch, err := manage.AddForward(manage.ForwardRequest{Tunnel: args[0], Ports: args[1], Proto: proto, To: to,
				Mode: mode.Mode, ProxyVersion: mode.ProxyVersion, Backup: mode.Backup, Listen: mode.Listen, Auth: mode.Auth})
			if err != nil {
				return err
			}
			printChange(ch)
			if mode.Mode == config.ForwardSocks {
				success("proxy on %s (SOCKS5 and HTTP): connections leave from the far end of %s", proxyAddr(mode.Listen, args[1]), args[0])
				return nil
			}
			target := "same port"
			if to != 0 {
				target = fmt.Sprintf("port %d", to)
			}
			success("forwarding %s %s → %s (%s)", proto, args[1], args[0], target)
			return nil
		},
	}
	add.Flags().StringVar(&proto, "proto", "both", "tcp, udp or both")
	add.Flags().Uint16Var(&to, "to", 0, "target port on the far end (default: same port)")
	forwardModeFlags(add.Flags(), &mode)
	list := &cobra.Command{
		Use:   "list",
		Short: "List port forwards",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			_, fs := st.Effective()
			title("Port forwards")
			if len(fs) == 0 {
				info("none")
			}
			paths := failover.Load()
			for i, f := range fs {
				to := ""
				if f.To != 0 {
					to = fmt.Sprintf(" :%d", f.To)
				}
				backup := ""
				if f.Backup != "" {
					backup = fmt.Sprintf("  (backup %s, now through %s)", f.Backup, paths.ActiveOf(f))
				}
				if f.ModeOf() == config.ForwardSocks {
					to = " (proxy on " + proxyAddr(f.Listen, f.Ports) + ")"
				}
				fmt.Printf("  %d. %-5s %-20s → %s%s%s\n", i+1, f.Proto, f.Ports, f.Tunnel, to, backup)
			}
			return nil
		},
	}
	rm := &cobra.Command{
		Use:   "remove <tunnel> <ports>",
		Short: "Remove a port forward",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := manage.RemoveForward(args[0], args[1])
			if err != nil {
				return err
			}
			printChange(ch)
			success("removed")
			return nil
		},
	}
	cmd.AddCommand(add, forwardSetCmd(), list, rm)
	return cmd
}
