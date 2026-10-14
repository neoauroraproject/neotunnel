package cli

import (
	"fmt"
	"strconv"
	"strings"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
	"neotunnel/internal/version"
)

type menuItem struct {
	key, label string
	run        func() error
}

func banner(st *config.State) {
	fmt.Println()
	fmt.Println(accent("  ╭─────────────────────────────────────────╮"))
	fmt.Printf("%s  %s %s%s\n", accent("  │"), bold("NeoTunnel"), dim(version.Version), strings.Repeat(" ", max(0, 29-len(version.Version)))+accent("│"))
	fmt.Println(accent("  ╰─────────────────────────────────────────╯"))
	ts, _ := st.Effective()
	role := "standalone"
	if st.Managed != nil {
		role = "managed by " + st.Managed.Master.Name
	} else if len(st.Peers) > 0 {
		role = fmt.Sprintf("master of %d peer(s)", len(st.Peers))
	}
	fmt.Printf("  %s %s  %s  %s\n", bold(st.Self), dim(st.Address), dim(role), dim(fmt.Sprintf("%d tunnel(s)", len(ts))))
}

func menu() error {
	if err := requireRoot(); err != nil {
		return err
	}
	items := []menuItem{
		{"1", "Web panel address and login", func() error { return run("panel", "info") }},
		{"2", "Quick tunnel (6to4 recommended)", menuQuick},
		{"3", "Status", func() error {
			st, err := loadNode()
			if err == nil {
				printStatus(st)
			}
			return err
		}},
		{"4", "Add a peer server", menuAddPeer},
		{"5", "Setup link for a peer", menuPeerLink},
		{"6", "Push changes to all peers", func() error { return run("peer", "sync") }},
		{"7", "Test a tunnel (ping + bandwidth)", func() error { return runAsk("test", "Tunnel name") }},
		{"8", "Benchmark all tunnel kinds to a peer", func() error { return runAsk("bench", "Peer name") }},
		{"9", "Live traffic", func() error { return run("traffic") }},
		{"10", "Remove a tunnel", func() error { return runAsk("tunnel", "Tunnel name", "remove") }},
		{"11", "Apply a setup link from a master", menuApplyLink},
		{"12", "Upgrade peers to this version", func() error { return run("peer", "upgrade") }},
		{"13", "Optimize network (BBR, buffers)", func() error { return run("optimize") }},
		{"14", "Install / restart service", func() error { return run("install") }},
	}
	for {
		st, err := loadNode()
		if err != nil {
			return err
		}
		banner(st)
		fmt.Println()
		for _, it := range items {
			fmt.Printf("  %s  %s\n", accent(fmt.Sprintf("%2s", it.key)), it.label)
		}
		fmt.Printf("  %s  %s\n", accent(" 0"), dim("Exit"))
		fmt.Println()
		choice := ask(accent("›")+" Choose", "")
		if choice == "0" || choice == "q" || choice == "" {
			return nil
		}
		found := false
		for _, it := range items {
			if it.key == choice {
				found = true
				if err := it.run(); err != nil {
					bad("%v", err)
				}
			}
		}
		if !found {
			warn("no option %q", choice)
		}
		ask(dim("Press Enter to continue"), "")
	}
}

// run invokes a subcommand in-process so the menu and CLI share one path.
func run(args ...string) error {
	root := cobraRoot()
	root.SetArgs(args)
	return root.Execute()
}

func runAsk(cmd, prompt string, sub ...string) error {
	v := ask(prompt, "")
	if v == "" {
		return nil
	}
	args := append([]string{cmd}, sub...)
	return run(append(args, v)...)
}

func menuQuick() error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	title("Quick tunnel")
	info("6to4 = SIT with private IPv6 + IPIP6 for IPv4. Fast, light, no extra software.")
	def := ""
	if len(st.Peers) == 1 {
		def = st.Peers[0].Name
	}
	peer := ask("Peer name (the other server)", def)
	if peer == "" {
		return nil
	}
	address := ""
	if _, ok := st.Peer(peer); !ok {
		address = ask("Its public IP", "")
	}
	req := manage.TunnelRequest{Peer: peer, Proto: "both"}
	req.Mode = ask("Mode ("+strings.Join(manage.ModeNames(), ", ")+")", "6to4")
	if p := ask("UDP port for the tunnel (empty = native protocol)", ""); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return fmt.Errorf("bad port %q", p)
		}
		req.Port = uint16(n)
	}
	req.Forward = ask("Ports to forward from this server (empty = none)", "")
	return quick(req, address)
}

func menuAddPeer() error {
	name := ask("Peer name (e.g. kharej1)", "")
	addr := ask("Its public IP", "")
	if name == "" || addr == "" {
		return nil
	}
	return run("peer", "add", name, "--address", addr)
}

func menuPeerLink() error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	printPeers(st)
	name := ask("Peer name", "")
	if name == "" {
		return nil
	}
	return showSetup(name)
}

func menuApplyLink() error {
	link := ask("Paste the neotunnel:// link", "")
	if link == "" {
		return nil
	}
	return applyLink(link, false)
}
