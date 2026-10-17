package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
)

func quickCmd() *cobra.Command {
	var req manage.TunnelRequest
	var address string
	cmd := &cobra.Command{
		Use:   "quick <peer>",
		Short: "One step: tunnel to a peer (6to4 by default) with optional port forwarding",
		Example: `  neotunnel quick germany --address 203.0.113.10 --forward 443,8443
  neotunnel quick germany --address 198.51.100.7 --port 3000 --forward 2053 --forward-to 443
  neotunnel quick germany --encrypt aes-gcm-128 --mtu-probe
  neotunnel quick germany --obfs --port 8443 --padding 32
  neotunnel quick germany --forward 443 --forward-mode transparent
  neotunnel quick germany --mode wireguard`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Peer = args[0]
			if st, err := loadNode(); err == nil {
				withDefaults(cmd.Flags(), st, &req.Settings, &req.Obfs)
				if !cmd.Flags().Changed("forward-mode") {
					req.ForwardMode = st.D().ForwardMode
				}
			}
			if req.ForwardMode == config.ForwardRelay && !cmd.Flags().Changed("proto") {
				req.Proto = "tcp" // relay mode carries TCP only
			}
			return quick(req, address)
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Mode, "mode", "6to4", "stack: "+strings.Join(manage.ModeNames(), ", "))
	f.StringVar(&req.Name, "name", "", "tunnel name (default: t1, t2, ...)")
	f.StringVar(&address, "address", "", "peer's public IP (adds the peer if it doesn't exist)")
	f.Uint16Var(&req.Port, "port", 0, "run the tunnel on this UDP port (lets several tunnels go to different servers)")
	f.StringVar(&req.Forward, "forward", "", "ports on this node to forward to the peer, e.g. 443,2053-2096")
	f.Uint16Var(&req.ForwardTo, "forward-to", 0, "target port on the peer (default: same port)")
	f.StringVar(&req.Proto, "proto", "both", "forwarded protocol: tcp, udp, both")
	f.StringVar(&req.ForwardMode, "forward-mode", "", "nat (default), transparent (keeps the client IP), proxy (PROXY protocol) or relay (TCP only, each connection carried as its own TCP connection; fastest on long, lossy paths)")
	f.IntVar(&req.ProxyVersion, "proxy-version", 0, "PROXY protocol version for --forward-mode proxy: 2 (default) or 1; with relay: 0 (none, default), 1 or 2")
	f.BoolVar(&req.Obfs, "obfs", false, "run the tunnel over NeoObfs (encrypted UDP that looks like noise; give --port)")
	settingsFlags(f, &req.Settings)
	return cmd
}

func quick(req manage.TunnelRequest, address string) error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	if _, exists := st.Peer(req.Peer); !exists {
		if address == "" {
			return fmt.Errorf("server %q is new: give its public IP with --address", req.Peer)
		}
		if err := manage.AddPeer(req.Peer, address); err != nil {
			return err
		}
		success("server %s added", req.Peer)
	}
	t, ch, err := manage.CreateTunnel(req)
	if err != nil {
		return err
	}
	printChange(ch)
	st, _ = loadNode()
	title(fmt.Sprintf("%s → %s (%s)", st.Self, req.Peer, req.Mode))
	for _, n := range manage.Stack(st, t.Name) {
		if tt, ok := st.Tunnel(n); ok {
			printTunnel(st, *tt)
		}
	}
	if req.Forward != "" {
		success("ports %s (%s) on this server now reach %s", req.Forward, req.Proto, req.Peer)
	}
	fmt.Println()
	info("measure it: neotunnel test %s", t.Name)
	return nil
}
