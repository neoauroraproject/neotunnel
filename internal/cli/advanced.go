package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
	"neotunnel/internal/service"
	"neotunnel/internal/traffic"
	"neotunnel/internal/web"
)

// settingsFlags registers the advanced tunnel options on a command.
func settingsFlags(f *pflag.FlagSet, s *manage.Settings) {
	f.IntVar(&s.MTU, "mtu", 0, "MTU of the tunnel (default: computed from the overhead)")
	f.BoolVar(&s.MTUProbe, "mtu-probe", false, "measure the path MTU to the far end and size the tunnel from it")
	f.Uint8Var(&s.TTL, "ttl", 0, "TTL of the outer packets (default 255)")
	f.StringVar(&s.Encrypt, "encrypt", "", "kernel IPsec on the outer packets (a TCP tunnel seals its stream instead): aes-gcm-128, aes-gcm-256, chacha20-poly1305")
	f.IntVar(&s.Padding, "padding", 0, "NeoObfs: up to this many random padding bytes per packet (0-255)")
	f.IntVar(&s.Keepalive, "keepalive", 0, "WireGuard/NeoObfs keepalive in seconds (-1 = off, default 25)")
	f.StringVar(&s.PeerKey, "peer-key", "", "WireGuard: the far end's public key, when it doesn't run NeoTunnel")
	f.StringVar(&s.Profile, "profile", "", "speed profile: lean (low latency, light traffic), balanced (default) or max (heavy traffic, uses every CPU core)")
	f.IntVar(&s.Lanes, "lanes", 0, fmt.Sprintf("GRE/GRETAP to a NeoTunnel server: run on this many parallel tunnels (1-%d), each flow on one, to use more CPU cores", config.MaxLanes))
	f.Var(tunnelID{&s.TunnelID}, "tunnel-id", "EoIP: the tunnel id, same as the MikroTik's tunnel-id (0-65535, 0-4095 on EoIPv6 without a port; default: from the name)")
}

// tunnelID is an optional uint16 flag: unset leaves the id derived.
type tunnelID struct{ p **uint16 }

func (v tunnelID) String() string {
	if v.p == nil || *v.p == nil {
		return ""
	}
	return strconv.Itoa(int(**v.p))
}

func (v tunnelID) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return fmt.Errorf("must be a number 0-65535")
	}
	id := uint16(n)
	*v.p = &id
	return nil
}

func (tunnelID) Type() string { return "id" }

// withDefaults fills options the user didn't give from the saved defaults.
func withDefaults(f *pflag.FlagSet, st *config.State, s *manage.Settings, obfs *bool) {
	d := st.D()
	if !f.Changed("encrypt") {
		s.Encrypt = d.Encrypt
	}
	if !f.Changed("mtu-probe") {
		s.MTUProbe = d.MTUProbe
	}
	if !f.Changed("padding") {
		s.Padding = d.Padding
	}
	if obfs != nil && !f.Changed("obfs") {
		*obfs = d.Obfs
	}
}

func tunnelSetCmd() *cobra.Command {
	var s manage.Settings
	var port uint16
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Change a tunnel's advanced settings (MTU, encryption, TTL, port, ...)",
		Example: `  neotunnel tunnel set t1 --encrypt aes-gcm-128
  neotunnel tunnel set t1 --mtu-probe
  neotunnel tunnel set t1 --mtu 1400 --ttl 64`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			// Unchanged flags keep the tunnel's current values.
			cur, _ := manage.SettingsOf(st, args[0])
			f := cmd.Flags()
			keep := func(name string, apply func()) {
				if !f.Changed(name) {
					apply()
				}
			}
			keep("mtu", func() { s.MTU = cur.MTU })
			keep("mtu-probe", func() { s.MTUProbe = cur.MTUProbe })
			keep("ttl", func() { s.TTL = cur.TTL })
			keep("encrypt", func() { s.Encrypt = cur.Encrypt })
			keep("padding", func() { s.Padding = cur.Padding })
			keep("keepalive", func() { s.Keepalive = cur.Keepalive })
			keep("peer-key", func() { s.PeerKey = cur.PeerKey })
			keep("tunnel-id", func() { s.TunnelID = cur.TunnelID })
			keep("profile", func() { s.Profile = cur.Profile })
			keep("lanes", func() { s.Lanes = cur.Lanes })
			if f.Changed("no-encrypt") {
				s.Encrypt = ""
			}
			ch, err := manage.UpdateTunnel(args[0], s, port)
			if err != nil {
				return err
			}
			printChange(ch)
			success("%s updated", args[0])
			return nil
		},
	}
	settingsFlags(cmd.Flags(), &s)
	cmd.Flags().Bool("no-encrypt", false, "turn IPsec off")
	cmd.Flags().Uint16Var(&port, "port", 0, "move the tunnel to another UDP port")
	return cmd
}

func mtuCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "mtu <tunnel>",
		Short: "Measure the path MTU under a tunnel and find its best MTU",
		Long: "Sends don't-fragment probes to the tunnel's far end to find the largest\n" +
			"packet the path carries, then subtracts every layer's overhead.\n" +
			"With --apply the tunnel follows the measurement from now on.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			info("probing the path to the far end…")
			rep, err := web.MeasureMTU(ctx, st, args[0], service.Reload)
			if err != nil {
				return err
			}
			title("MTU of " + args[0])
			fmt.Printf("  path to %-16s %s\n", rep.Remote, bold(fmt.Sprint(rep.PathMTU)))
			fmt.Printf("  tunnel overhead          %d\n", rep.Overhead)
			fmt.Printf("  best tunnel MTU          %s\n", green(fmt.Sprint(rep.Best)))
			fmt.Printf("  current                  %d\n", rep.Current)
			if !apply {
				if !rep.Probing {
					info("apply it: neotunnel mtu %s --apply", args[0])
				}
				return nil
			}
			cur, _ := manage.SettingsOf(st, args[0])
			cur.MTUProbe, cur.MTU = true, 0
			ch, err := manage.UpdateTunnel(args[0], cur, 0)
			if err != nil {
				return err
			}
			printChange(ch)
			success("%s now follows the measured path MTU", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "size the tunnel from the measurement from now on")
	return cmd
}

func forwardSetCmd() *cobra.Command {
	var r manage.ForwardRequest
	cmd := &cobra.Command{
		Use:   "set <tunnel> <ports>",
		Short: "Change a forward's mode (nat, transparent, proxy, relay), target port, protocol or backup tunnel",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			r.Tunnel, r.Ports = args[0], args[1]
			_, fs := st.Effective()
			f := cmd.Flags()
			for _, fw := range fs {
				if fw.Tunnel == r.Tunnel && fw.Ports == r.Ports {
					if !f.Changed("proto") {
						r.Proto = fw.Proto
					}
					if !f.Changed("to") {
						r.To = fw.To
					}
					if !f.Changed("mode") {
						r.Mode = fw.Mode
					}
					if !f.Changed("proxy-version") {
						r.ProxyVersion = fw.ProxyVersion
					}
					if !f.Changed("backup") {
						r.Backup = fw.Backup
					}
				}
			}
			ch, err := manage.UpdateForward(r)
			if err != nil {
				return err
			}
			printChange(ch)
			success("forward %s on %s updated", r.Ports, r.Tunnel)
			return nil
		},
	}
	forwardModeFlags(cmd.Flags(), &r)
	cmd.Flags().StringVar(&r.Proto, "proto", "both", "tcp, udp or both")
	cmd.Flags().Uint16Var(&r.To, "to", 0, "target port on the far end (default: same port)")
	return cmd
}

func forwardModeFlags(f *pflag.FlagSet, r *manage.ForwardRequest) {
	f.StringVar(&r.Mode, "mode", "", "nat (default), transparent (far end sees the client's IP), proxy (PROXY protocol header) or relay (TCP only: each connection is carried to the far node as its own TCP connection, no tunnel packets; fastest on long, lossy paths)")
	f.IntVar(&r.ProxyVersion, "proxy-version", 0, "PROXY protocol version for --mode proxy: 2 (default, TCP+UDP) or 1 (TCP); with --mode relay: 0 (no header, default), 1 or 2")
	f.StringVar(&r.Backup, "backup", "", "a second tunnel to the same far server: the forward moves there while its tunnel stops passing traffic, and back once it has worked again for about a minute (set \"\" to remove)")
}

// resetRequest asks the daemon, which owns the counters, to zero them.
func trafficResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset [tunnel]",
		Short: "Zero the traffic totals of one tunnel or all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if err := traffic.RequestReset(name); err != nil {
				return err
			}
			service.Reload()
			success("traffic totals reset")
			return nil
		},
	}
}

// limitsOf reads a tunnel's current switch and caps.
func limitsOf(name string) (manage.Limits, error) {
	st, err := loadNode()
	if err != nil {
		return manage.Limits{}, err
	}
	t, ok := st.Tunnel(name)
	if !ok {
		return manage.Limits{}, fmt.Errorf("unknown tunnel %q", name)
	}
	return manage.LimitsOf(*t), nil
}

func tunnelLimitCmd() *cobra.Command {
	var rate int
	var quota float64
	cmd := &cobra.Command{
		Use:   "limit <name>",
		Short: "Set a tunnel's speed limit and monthly traffic quota (0 = none)",
		Example: `  neotunnel tunnel limit t1 --rate 100        # 100 Mbit/s each way
  neotunnel tunnel limit t1 --quota 500       # 500 GB a month, then down until next month
  neotunnel tunnel limit t1 --rate 0 --quota 0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := limitsOf(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("rate") {
				l.RateMbit = rate
			}
			if cmd.Flags().Changed("quota") {
				l.QuotaGB = quota
			}
			ch, err := manage.SetLimits(args[0], l)
			if err != nil {
				return err
			}
			printChange(ch)
			success("%s: speed limit %s, monthly quota %s", args[0],
				orNone(l.RateMbit > 0, fmt.Sprintf("%d Mbit/s", l.RateMbit)), orNone(l.QuotaGB > 0, fmt.Sprintf("%g GB", l.QuotaGB)))
			return nil
		},
	}
	cmd.Flags().IntVar(&rate, "rate", 0, "speed limit in Mbit/s, applied on both ends (0 = none)")
	cmd.Flags().Float64Var(&quota, "quota", 0, "monthly traffic in GB, download + upload (0 = none)")
	return cmd
}

func orNone(set bool, s string) string {
	if set {
		return s
	}
	return "none"
}

func tunnelPowerCmd(on bool) *cobra.Command {
	use, short := "disable <name>", "Take a tunnel down on both ends, keeping its settings"
	if on {
		use, short = "enable <name>", "Bring a disabled tunnel back up"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := limitsOf(args[0])
			if err != nil {
				return err
			}
			l.Disabled = !on
			ch, err := manage.SetLimits(args[0], l)
			if err != nil {
				return err
			}
			printChange(ch)
			success("%s %s", args[0], map[bool]string{true: "enabled", false: "disabled"}[on])
			return nil
		},
	}
}
