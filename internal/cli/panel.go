package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"neotunnel/internal/config"
	"neotunnel/internal/firewall"
	"neotunnel/internal/service"
	"neotunnel/internal/web"
)

func panelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "panel", Short: "Set up and manage the web panel"}

	var port int
	var user, password string
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Enable the panel (random free port and password unless given)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if _, err := loadNode(); err != nil {
				return err
			}
			c, err := web.Setup(port, user, password)
			if err != nil {
				return err
			}
			restartIfRunning()
			printCredentials(c)
			return nil
		},
	}
	setup.Flags().IntVar(&port, "port", 0, "panel port (default: keep current, or a random free one)")
	setup.Flags().StringVar(&user, "user", "", "login name (default: admin)")
	setup.Flags().StringVar(&password, "password", "", "password (default: generated)")

	info := &cobra.Command{
		Use:   "info",
		Short: "Show the panel address and login name",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadNode()
			if err != nil {
				return err
			}
			if st.Panel == nil {
				return fmt.Errorf("panel is not set up: sudo neotunnel panel setup")
			}
			printCredentials(web.Credentials{URL: web.PanelURL(st), User: st.Panel.User})
			fmt.Println(dim("  Forgot the password? sudo neotunnel panel password"))
			return nil
		},
	}

	passwd := &cobra.Command{
		Use:   "password [new-password]",
		Short: "Reset the panel password (generated if not given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			p := ""
			if len(args) == 1 {
				p = args[0]
			}
			c, err := web.Setup(0, "", p)
			if err != nil {
				return err
			}
			printCredentials(c)
			return nil
		},
	}

	setPort := &cobra.Command{
		Use:   "port <port>",
		Short: "Move the panel to another port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			p, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("bad port %q", args[0])
			}
			st, err := config.Load()
			if err != nil {
				return err
			}
			if st.Panel == nil {
				return fmt.Errorf("panel is not set up: sudo neotunnel panel setup --port %d", p)
			}
			if !web.PortFree(p) {
				return fmt.Errorf("port %d is in use or reserved", p)
			}
			st, err = config.Update(func(s *config.State) error { s.Panel.Port = p; return nil })
			if err != nil {
				return err
			}
			restartIfRunning()
			success("panel moved to %s", web.PanelURL(st))
			return nil
		},
	}

	disable := &cobra.Command{
		Use:   "disable",
		Short: "Turn the panel off",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			if _, err := config.Update(func(s *config.State) error { s.Panel = nil; return nil }); err != nil {
				return err
			}
			restartIfRunning()
			success("panel disabled")
			return nil
		},
	}

	cmd.AddCommand(setup, info, passwd, setPort, disable)
	return cmd
}

func restartIfRunning() {
	if service.Installed() {
		service.Restart()
	}
}

func printCredentials(c web.Credentials) {
	title("NeoTunnel panel")
	fmt.Printf("  %s  %s\n", dim("address "), bold(c.URL))
	fmt.Printf("  %s  %s\n", dim("username"), bold(c.User))
	if c.Password != "" {
		fmt.Printf("  %s  %s\n", dim("password"), bold(c.Password))
	}
	fmt.Println()
	fmt.Println(dim("  The certificate is self-signed: accept the browser warning once."))
	if firewall.Active() {
		fmt.Println(dim("  ufw is active; the service opens the panel port automatically."))
	}
}
