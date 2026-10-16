package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"neotunnel/internal/engine"
	"neotunnel/internal/sysctl"
)

func doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the system and clean up anything NeoTunnel left behind",
		Long: "Looks for leftover interfaces, IPsec state, routing rules and UDP\n" +
			"ports that no current tunnel needs, and checks kernel modules and\n" +
			"network tuning. With --fix it removes the leftovers.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireRoot(); err != nil {
				return err
			}
			st, err := loadNode()
			if err != nil {
				return err
			}
			title("NeoTunnel doctor")
			ok, warn := 0, 0
			show := func(fs []engine.Finding) {
				for _, f := range fs {
					switch {
					case f.Fixed:
						success("%s", f.Message)
					case f.OK:
						success("%s", f.Message)
						ok++
					default:
						bad("%s", f.Message)
						warn++
					}
				}
			}
			show(engine.Doctor(st, fix))
			show(engine.DoctorFOU(st, fix))

			title("Kernel modules")
			for _, m := range []string{"sit", "ipip", "ip_gre", "ip6_tunnel", "vxlan", "geneve", "fou", "wireguard", "tun", "xfrm_user", "esp4"} {
				if moduleAvailable(m) {
					success("%s", m)
				} else {
					warn++
					bad("%s not available (modprobe %s, or install linux-modules-extra)", m, m)
				}
			}

			title("Network tuning")
			if cc, _ := sysctl.Get("net.ipv4.tcp_congestion_control"); cc == "bbr" {
				success("BBR is on")
			} else {
				warn++
				bad("BBR is off; run: sudo neotunnel boost")
			}
			if v, _ := sysctl.Get("net.ipv4.ip_forward"); v == "1" {
				success("IP forwarding is on")
			} else {
				warn++
				bad("IP forwarding is off")
			}

			fmt.Println()
			if warn == 0 {
				success("everything looks healthy")
			} else if !fix {
				info("%d issue(s); run 'sudo neotunnel doctor --fix' to clean up what can be removed", warn)
			} else {
				info("%d issue(s) remain (modules or tuning may need attention)", warn)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "remove the leftovers it finds")
	return cmd
}

// moduleAvailable reports whether a kernel module is loaded or loadable.
func moduleAvailable(m string) bool {
	if b, err := os.ReadFile("/proc/modules"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, m+" ") {
				return true
			}
		}
	}
	// Built into the kernel, or loadable.
	if exec.Command("modprobe", "-nq", m).Run() == nil {
		return true
	}
	// Some tunnel types (sit, ipip) are often builtin; check /sys.
	_, err := os.Stat("/sys/module/" + m)
	return err == nil
}
