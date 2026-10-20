package cli

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/forward"
	"neotunnel/internal/manage"
	"neotunnel/internal/service"
	"neotunnel/internal/setuplink"
	"neotunnel/internal/sysctl"
	"neotunnel/internal/version"
	"neotunnel/internal/web"
)

// The interactive menu: everything the panel does, from the terminal.

var modeHints = map[string]string{
	"6to4":      "SIT + IPIP6: light and fast (recommended)",
	"6to4-gre":  "SIT + GRE6: carries both IPv4 and IPv6",
	"ipip":      "plain IPv4 in IPv4, the lowest overhead",
	"gre":       "standard GRE, works with MikroTik and most routers",
	"sit":       "IPv6 only, over IPv4",
	"gretap":    "layer 2 (Ethernet) over GRE",
	"vxlan":     "layer 2 over UDP",
	"geneve":    "layer 2 over UDP, newer than VXLAN",
	"wireguard": "encrypted in the kernel; also talks to MikroTik v7",
	"obfs":      "NeoObfs: encrypted UDP that looks like random noise",
	"tcp":       "packets over TCP: a last resort when UDP and GRE/SIT are blocked",
	"l2tpv3":    "layer 2 pseudowire over IP or UDP, MikroTik compatible",
	"eoip":      "MikroTik EoIP / EoIPv6: layer 2 over GRE, IPv6 or UDP",
}

type session struct {
	t    *tui
	quit bool // ctrl+c anywhere, or after uninstalling
}

// choose wraps tui.choose: ctrl+c leaves the whole menu.
func (s *session) choose(title string, items []item, sel int) int {
	c := s.t.choose(s.header(), title, items, sel)
	if c == -2 {
		s.quit = true
	}
	return c
}

// run invokes a subcommand in-process so the menu and the CLI share one path.
func run(args ...string) error {
	root := cobraRoot()
	root.SetArgs(args)
	return root.Execute()
}

func menu() error {
	if err := requireRoot(); err != nil {
		return err
	}
	t, err := openTUI()
	if err != nil {
		fmt.Println("No terminal for the interactive menu; see: neotunnel --help")
		return nil
	}
	defer t.close()
	if _, err := loadNode(); err != nil {
		return err
	}
	(&session{t: t}).main()
	t.cooked()
	fmt.Print(clearScreen())
	fmt.Println(dim("  NeoTunnel menu closed. Open it again with: ") + bold("neotunnel"))
	return nil
}

// header is drawn on top of every screen.
func (s *session) header() string {
	st, err := config.Load()
	if err != nil {
		return " " + red(err.Error()) + "\n\n"
	}
	line := dim(" " + strings.Repeat("─", min(64, s.t.width()-2)))
	svc := red("● stopped")
	if exec.Command("systemctl", "is-active", "--quiet", service.Name).Run() == nil {
		svc = green("● running")
	}
	ts := visible(st)
	up := 0
	for _, t := range ts {
		if engine.State(engine.Ifname(t.Name)).Up {
			up++
		}
	}
	paired := 0
	for _, p := range st.Peers {
		if p.Paired() {
			paired++
		}
	}
	pad := func(s string, n int) string { return s + strings.Repeat(" ", max(1, n-len(s))) }
	var b strings.Builder
	b.WriteString("\n " + accent("◆ NeoTunnel") + " " + dim(version.Version) + "   " + dim(strings.TrimPrefix(version.Channel, "https://")) + "\n")
	b.WriteString(line + "\n")
	self := shown(st.Label, st.Self)
	b.WriteString(" " + dim("server ") + "  " + bold(self) + "  " + pad(st.Address, 22-utf8.RuneCountInString(self)) + dim("service ") + " " + svc + "\n")
	b.WriteString(" " + dim("tunnels") + "  " + pad(fmt.Sprintf("%d of %d up", up, len(ts)), 24) + dim("servers ") + " " + fmt.Sprintf("%d of %d connected", paired, len(st.Peers)) + "\n")
	for _, m := range st.Masters() {
		state := green("connected")
		if m.EnrollToken != "" {
			state = yellow("waiting to reach it")
		}
		b.WriteString(" " + dim("main   ") + "  " + m.Master.Name + " (" + strings.Split(m.Master.Address, ":")[0] + ")  " + state + "\n")
	}
	if st.Panel != nil {
		b.WriteString(" " + dim("panel  ") + "  " + web.PanelURL(st) + "\n")
	}
	b.WriteString(line + "\n\n")
	return b.String()
}

// visible lists the user-facing tunnels (carriers of a stack are hidden).
func visible(st *config.State) []config.Tunnel {
	ts, _ := st.Effective()
	carrier := map[string]bool{}
	for _, t := range ts {
		if t.Over != "" {
			carrier[t.Over] = true
		}
	}
	var out []config.Tunnel
	for _, t := range ts {
		if !carrier[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

func (s *session) page(title string, fn func() error) { s.t.page(s.header(), title, fn) }

// form clears the screen for a set of questions.
func (s *session) form(title, intro string) {
	s.t.rawMode()
	s.t.write(clearScreen() + s.header() + " " + bold(title) + "\n\n")
	if intro != "" {
		s.t.write(dim(intro) + "\n\n")
	}
}

func (s *session) main() {
	sel := 0
	for !s.quit {
		st, _ := config.Load()
		items := []item{
			{"Create a tunnel", "6to4, IPIP, GRE, VXLAN… optionally on its own UDP port", false},
			{"Tunnels", fmt.Sprintf("%d · details, speed test, port forward, remove", len(visible(st))), false},
			{"Destination servers", fmt.Sprintf("%d · add, connect command, sync, compare speeds", len(st.Peers)), false},
			{"Port forwarding", fmt.Sprintf("%d · send ports of this server through a tunnel", len(st.Forwards)), false},
			{"Speed test", "one tunnel, or compare every tunnel kind", false},
			{"Live traffic", "rates and today's, month's and total traffic per tunnel", false},
			{"Join a main server", "paste the setup key shown by your main server", false},
			{"Web panel", "address, password, port", false},
			{"System", "name & IP, network boost, re-apply, restart the service", false},
			{"Update NeoTunnel", "install the latest release; tunnels and settings stay", false},
			{"Uninstall NeoTunnel", "remove tunnels, rules, settings and the program", false},
		}
		sel = s.choose("Main menu", items, sel)
		if s.quit {
			return
		}
		switch sel {
		case 0:
			s.createTunnel("")
		case 1:
			s.tunnels()
		case 2:
			s.servers()
		case 3:
			s.forwards()
		case 4:
			s.speed()
		case 5:
			s.page("Live traffic", showTraffic)
		case 6:
			s.join()
		case 7:
			s.panel()
		case 8:
			s.system()
		case 9:
			s.update()
		case 10:
			if s.uninstall() {
				return
			}
		default:
			return
		}
	}
}

// update installs the latest release over this one; settings are kept.
func (s *session) update() {
	if !s.t.confirm("Download and install the latest NeoTunnel release? (tunnels and settings are kept)", true) {
		return
	}
	s.page("Update NeoTunnel", func() error { return selfUpdate("") })
}

// uninstall removes NeoTunnel after two questions; it reports whether the
// menu must close.
func (s *session) uninstall() bool {
	if !s.t.confirm(red("Remove NeoTunnel completely: tunnels, service and the program?"), false) {
		return false
	}
	keep := s.t.confirm("Keep the configuration and keys for a later reinstall?", false)
	s.page("Uninstall", func() error {
		uninstall(keep)
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	s.quit = true
	return true
}

// ---------- create a tunnel ----------

// wizardScreen draws the steps chosen so far above the current question.
func (s *session) wizardScreen(step int, done []string) {
	s.t.rawMode()
	var b strings.Builder
	b.WriteString(clearScreen() + s.header())
	b.WriteString(" " + bold("Create a tunnel") + dim(fmt.Sprintf("  step %d of 6", step)) + "\n\n")
	for _, d := range done {
		b.WriteString("   " + green("✓ ") + d + "\n")
	}
	if len(done) > 0 {
		b.WriteString("\n")
	}
	s.t.write(b.String())
}

func validIP(v string) error {
	if _, err := netip.ParseAddr(v); err != nil {
		return fmt.Errorf("enter an IP address")
	}
	return nil
}

func validPort(v string) error {
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("enter a port between 1 and 65535, or leave it empty")
	}
	return nil
}

func chooseProto(s *session, title string) (string, bool) {
	protos := []string{"both", "tcp", "udp"}
	c := s.choose(title, []item{
		{"TCP and UDP", "", false}, {"TCP only", "", false}, {"UDP only", "", false}}, 0)
	if c < 0 {
		return "", false
	}
	return protos[c], true
}

func (s *session) createTunnel(peer string) {
	st, err := loadNode()
	if err != nil {
		return
	}
	req := manage.TunnelRequest{Peer: peer, Proto: "both"}
	var done []string
	newServer, newIP := "", ""

	// 1. Destination.
	if peer == "" {
		var items []item
		for _, p := range st.Peers {
			state := "connected"
			if !p.Paired() {
				state = "not connected yet"
			}
			items = append(items, item{shown(p.Label, p.Name), p.Address + " · " + state, false})
		}
		items = append(items,
			item{"New server", "NeoTunnel gets installed on it with one command", false},
			item{"Other device", "MikroTik or any router: only this side is created", false})
		c := s.choose("Create a tunnel · step 1 of 6 · where should it go?", items, 0)
		switch {
		case c < 0:
			return
		case c < len(st.Peers):
			req.Peer = st.Peers[c].Name
		case c == len(st.Peers):
			s.wizardScreen(1, nil)
			name, ok := s.t.input("Name for the new server (e.g. germany)", "", func(v string) error {
				if err := manage.ValidNodeName(v); err != nil {
					return err
				}
				if _, exists := st.Peer(v); exists {
					return fmt.Errorf("a server named %s already exists", v)
				}
				return nil
			})
			if !ok {
				return
			}
			ip, ok := s.t.input("Its public IP", "", validIP)
			if !ok {
				return
			}
			newServer, newIP, req.Peer = name, ip, name
		default:
			s.wizardScreen(1, nil)
			ip, ok := s.t.input("IP of the other device", "", validIP)
			if !ok {
				return
			}
			req.Remote = ip
		}
	}
	switch {
	case newServer != "":
		done = append(done, fmt.Sprintf("destination  %s (%s), new server", newServer, newIP))
	case req.Peer != "":
		p, _ := st.Peer(req.Peer)
		done = append(done, fmt.Sprintf("destination  %s (%s)", p.Name, p.Address))
	default:
		done = append(done, "destination  "+req.Remote+" (other device)")
	}

	// 2. Kind.
	modes := manage.Modes()
	items := make([]item, len(modes))
	for i, m := range modes {
		items[i] = item{m.Name, modeHints[m.Name], false}
	}
	c := s.choose("Create a tunnel · step 2 of 6 · tunnel kind", items, 0)
	if c < 0 {
		return
	}
	mode := modes[c]
	req.Mode = mode.Name
	done = append(done, "kind         "+mode.Title)

	// 3. Protection: kernel IPsec or a NeoObfs layer (both need NeoTunnel
	// on the far end; encrypted kinds need neither).
	d := st.D()
	req.MTUProbe = d.MTUProbe
	if req.Remote == "" && !mode.Crypt {
		def := 0
		switch {
		case d.Obfs:
			def = 2
		case d.Encrypt != "":
			def = 1
		}
		choices := []item{
			{"None", "plain tunnel, the fastest", false},
			{"Encrypt (IPsec)", "AES-GCM in the kernel, keys from the tunnel secret", false},
			{"Obfuscate (NeoObfs)", "encrypted UDP that looks like random noise; needs a port", false},
		}
		if mode.TCP {
			// The TCP transport seals its own frames; NeoObfs is UDP.
			choices = choices[:2]
			choices[1] = item{"Encrypt", "AES-GCM on the TCP stream, keys from the tunnel secret", false}
			def = min(def, 1)
		}
		c := s.choose("Create a tunnel · step 3 of 6 · protection", choices, def)
		switch c {
		case -1:
			return
		case 1:
			req.Encrypt = d.Encrypt
			if req.Encrypt == "" {
				req.Encrypt = "aes-gcm-128"
			}
			if mode.TCP {
				done = append(done, "protection   "+req.Encrypt)
			} else {
				done = append(done, "protection   IPsec "+req.Encrypt)
			}
		case 2:
			req.Obfs, req.Padding = true, d.Padding
			done = append(done, "protection   NeoObfs")
		default:
			done = append(done, "protection   none")
		}
	}

	if mode.Name == "wireguard" && req.Remote != "" {
		s.wizardScreen(3, done)
		key, ok := s.t.input("WireGuard public key of the other device", "", func(v string) error {
			if b, err := base64.StdEncoding.DecodeString(v); err != nil || len(b) != 32 {
				return fmt.Errorf("a WireGuard public key is 44 characters of base64")
			}
			return nil
		})
		if !ok {
			return
		}
		req.PeerKey = key
		done = append(done, "peer key     "+key[:8]+"…")
	}
	if mode.Name == "eoip" && req.Remote != "" {
		s.wizardScreen(3, done)
		v, ok := s.t.input("Tunnel ID, same as the MikroTik's tunnel-id (empty = from the name)", "", func(v string) error {
			if v == "" {
				return nil
			}
			if _, err := strconv.ParseUint(v, 10, 16); err != nil {
				return fmt.Errorf("a tunnel id is 0-65535 (0-4095 on EoIPv6 without a port)")
			}
			return nil
		})
		if !ok {
			return
		}
		if v != "" {
			n, _ := strconv.ParseUint(v, 10, 16)
			id := uint16(n)
			req.TunnelID = &id
			done = append(done, "tunnel id    "+v)
		}
	}

	// 4. Port.
	s.wizardScreen(4, done)
	portQ := "UDP port for the tunnel (empty = native protocol, fastest)"
	switch {
	case req.Obfs:
		portQ = "UDP port for NeoObfs (empty = a free port)"
	case mode.UDP:
		portQ = "UDP port (empty = the standard or a free port)"
	case mode.TCP:
		portQ = "TCP port both servers listen on (empty = a free port)"
	}
	if mode.TCP {
		s.t.write(dim("   TCP inside TCP slows down a lot under packet loss: use it only when\n   neither UDP nor protocol 41/47 passes between the two servers.") + "\n\n")
	} else {
		s.t.write(dim("   A UDP port helps when the path blocks protocol 41/47, and lets\n   tunnels to different servers each run on their own port.") + "\n\n")
	}
	port, ok := s.t.input(portQ, "", validPort)
	if !ok {
		return
	}
	if port != "" {
		n, _ := strconv.Atoi(port)
		req.Port = uint16(n)
		if mode.TCP {
			done = append(done, "tcp port     "+port)
		} else {
			done = append(done, "udp port     "+port)
		}
	} else if mode.TCP {
		done = append(done, "tcp port     automatic")
	} else {
		done = append(done, "udp port     none (native protocol)")
	}

	// 5. Forwarding.
	s.wizardScreen(5, done)
	fwd, ok := s.t.input("Ports of this server to forward through it (e.g. 443,2053-2083; empty = none)", "", func(v string) error {
		if v == "" {
			return nil
		}
		_, err := forward.ParsePorts(v)
		return err
	})
	if !ok {
		return
	}
	if fwd != "" {
		req.Forward = fwd
		to, ok := s.t.input("Target port on the other side (empty = same port)", "", validPort)
		if !ok {
			return
		}
		if to != "" {
			n, _ := strconv.Atoi(to)
			req.ForwardTo = uint16(n)
		}
		if req.Proto, ok = chooseProto(s, "Create a tunnel · step 5 of 6 · forwarded protocol"); !ok {
			return
		}
		target := "same port"
		if to != "" {
			target = "port " + to
		}
		done = append(done, fmt.Sprintf("forward      %s (%s) → %s", fwd, req.Proto, target))
	} else {
		done = append(done, "forward      none")
	}

	// 6. Name and confirm.
	s.wizardScreen(6, done)
	name, ok := s.t.input("Tunnel name", manage.FreeName(st), func(v string) error {
		if err := config.ValidName(v); err != nil {
			return err
		}
		if (len(mode.Kinds) > 1 || req.Obfs) && len(v) > 8 {
			return fmt.Errorf("at most 8 characters for a %s tunnel", mode.Name)
		}
		return nil
	})
	if !ok {
		return
	}
	req.Name = name
	if !s.t.confirm("Create tunnel "+bold(name)+"?", true) {
		return
	}

	s.page("Creating tunnel "+name, func() error {
		if newServer != "" {
			if err := manage.AddPeer(newServer, newIP); err != nil {
				return err
			}
			success("server %s added", newServer)
		}
		t, ch, err := manage.CreateTunnel(req)
		if err != nil {
			return err
		}
		printChange(ch)
		st, _ := loadNode()
		fmt.Println()
		for _, n := range manage.Stack(st, t.Name) {
			if tt, ok := st.Tunnel(n); ok {
				printTunnel(st, *tt)
			}
		}
		if req.Forward != "" {
			fmt.Println()
			success("ports %s of this server now reach the other side through %s", req.Forward, t.Name)
		}
		if req.Remote != "" {
			fmt.Println()
			info("set up the same %s tunnel on %s with the addresses above", mode.Name, req.Remote)
			if mode.Name == "wireguard" {
				pub, _ := engine.WireGuardPublicKey(t, 0)
				info("this server's WireGuard public key: %s (port %d)", bold(pub), t.Port)
			}
		}
		return nil
	})
}

// ---------- tunnels ----------

func (s *session) tunnels() {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		ts := visible(st)
		var items []item
		for _, t := range ts {
			items = append(items, tunnelItem(st, t))
		}
		items = append(items, item{"+ Create a tunnel", "", false})
		sel = s.choose("Tunnels", items, sel)
		if s.quit {
			return
		}
		switch {
		case sel < 0:
			return
		case sel == len(ts):
			s.createTunnel("")
		default:
			s.tunnelActions(ts[sel].Name)
		}
	}
}

func tunnelItem(st *config.State, t config.Tunnel) item {
	state := "down"
	if engine.State(engine.Ifname(t.Name)).Up {
		state = "up"
	}
	kind := t.Mode
	if kind == "" {
		kind = string(t.Kind)
	}
	hint := fmt.Sprintf("%s → %s · %s", kind, farName(st, t), state)
	if p := stackPort(st, t); p != 0 {
		hint += fmt.Sprintf(" · udp %d", p)
	}
	if t.Label != "" {
		hint += " · " + t.Name
	}
	return item{shown(t.Label, t.Name), hint, false}
}

// shown is a display name: the label when set, else the ID.
func shown(label, id string) string {
	if label != "" {
		return label
	}
	return id
}

// nodeLabel is a server's display name: a peer's or this node's label.
func nodeLabel(st *config.State, name string) string {
	if p, ok := st.Peer(name); ok {
		return shown(p.Label, p.Name)
	}
	if st.IsSelf(name) {
		return shown(st.Label, st.Self)
	}
	return name
}

func farName(st *config.State, t config.Tunnel) string {
	me := 0
	if st.IsSelf(t.Sides[1].Node) {
		me = 1
	}
	if n := t.Sides[1-me].Node; n != "" {
		return nodeLabel(st, n)
	}
	for _, n := range manage.Stack(st, t.Name) {
		if bt, ok := st.Tunnel(n); ok && bt.Over == "" {
			return bt.Sides[1-me].Endpoint
		}
	}
	return t.Sides[1-me].Endpoint
}

// stackPort is the UDP port of a tunnel or of the carrier it runs over.
func stackPort(st *config.State, t config.Tunnel) uint16 {
	ts, _ := st.Effective()
	for _, n := range manage.Stack(st, t.Name) {
		for _, x := range ts {
			if x.Name == n && x.Port != 0 {
				return x.Port
			}
		}
	}
	return 0
}

func (s *session) tunnelActions(name string) {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		_, local := st.Tunnel(name)
		items := []item{
			{"Details", "addresses, MTU, traffic, forwards", false},
			{"Speed test", "ping, jitter, loss, download and upload", !local},
			{"Forward ports through it", "", !local},
			{"Remove", "on both servers", !local},
		}
		sel = s.choose("Tunnel "+name, items, sel)
		if s.quit {
			return
		}
		switch sel {
		case 0:
			s.page("Tunnel "+name, func() error { return tunnelDetails(name) })
		case 1:
			s.page("Speed test · "+name, func() error { return runTest(name, bench.DefaultOptions()) })
		case 2:
			s.addForward(name)
		case 3:
			if s.t.confirm("Remove tunnel "+bold(name)+" on both servers?", false) {
				s.page("Removing "+name, func() error {
					drop, ch, err := manage.RemoveTunnel(name)
					if err != nil {
						return err
					}
					printChange(ch)
					success("removed %s", strings.Join(drop, ", "))
					return nil
				})
				return
			}
		default:
			return
		}
	}
}

func tunnelDetails(name string) error {
	st, err := loadNode()
	if err != nil {
		return err
	}
	fmt.Println()
	ts, fs := st.Effective()
	for _, n := range manage.Stack(st, name) {
		for _, t := range ts {
			if t.Name == n {
				printTunnel(st, t)
			}
		}
	}
	for _, f := range fs {
		if f.Tunnel == name {
			to := "same port"
			if f.To != 0 {
				to = fmt.Sprintf("port %d", f.To)
			}
			fmt.Printf("      %s %s %s → %s\n", dim("forward"), f.Ports, dim("("+f.Proto+")"), to)
		}
	}
	return nil
}

// ---------- servers ----------

func (s *session) servers() {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		var items []item
		for _, p := range st.Peers {
			state := "connected"
			switch {
			case !p.Paired():
				state = "not connected yet"
			case p.AppliedRevision != st.Revision:
				state = "connected, behind"
			}
			items = append(items, item{shown(p.Label, p.Name), p.Address + " · " + state, false})
		}
		items = append(items, item{"+ Add a server", "", false})
		sel = s.choose("Destination servers", items, sel)
		if s.quit {
			return
		}
		switch {
		case sel < 0:
			return
		case sel == len(st.Peers):
			s.addServer()
		default:
			s.serverActions(st.Peers[sel].Name)
		}
	}
}

func (s *session) addServer() {
	s.form("Add a server", "   The next screen shows a command to run on that server; it installs\n   NeoTunnel there and connects it to this one.")
	st, _ := loadNode()
	name, ok := s.t.input("Name (e.g. germany)", "", func(v string) error {
		if err := manage.ValidNodeName(v); err != nil {
			return err
		}
		if _, exists := st.Peer(v); exists {
			return fmt.Errorf("a server named %s already exists", v)
		}
		return nil
	})
	if !ok {
		return
	}
	ip, ok := s.t.input("Public IP", "", validIP)
	if !ok {
		return
	}
	s.page("Server "+name, func() error {
		if err := manage.AddPeer(name, ip); err != nil {
			return err
		}
		success("server %s added", name)
		fmt.Println()
		return showSetup(name)
	})
}

func (s *session) serverActions(name string) {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		p, ok := st.Peer(name)
		if !ok {
			return
		}
		items := []item{
			{"Connect command", "run it on " + name + " to install and connect, or to catch up", false},
			{"Create a tunnel to it", "", false},
			{"Push configuration now", "", !p.Paired()},
			{"Compare tunnel speeds", "tries every kind and ranks them", !p.Paired()},
			{"Upgrade it to this version", version.Version, !p.Paired()},
			{"Remove", "forget it and remove this side's tunnels", false},
		}
		sel = s.choose("Server "+name+" · "+p.Address, items, sel)
		if s.quit {
			return
		}
		switch sel {
		case 0:
			s.page("Connect command for "+name, func() error { fmt.Println(); return showSetup(name) })
		case 1:
			s.createTunnel(name)
		case 2:
			s.page("Push configuration to "+name, func() error {
				printChange(manage.Change{Peers: manage.Sync(st, []string{name})})
				return nil
			})
		case 3:
			s.page("Compare tunnel speeds · "+name, func() error { return runBench(name, bench.DefaultOptions(), nil) })
		case 4:
			s.page("Upgrade "+name, func() error { return run("peer", "upgrade", name) })
		case 5:
			if s.t.confirm("Remove server "+bold(name)+"? (nothing is changed on it)", false) {
				s.page("Removing "+name, func() error {
					ch, err := manage.RemovePeer(name)
					if err != nil {
						return err
					}
					printChange(ch)
					success("server %s removed", name)
					return nil
				})
				return
			}
		default:
			return
		}
	}
}

// ---------- port forwarding ----------

func localTunnels(st *config.State) []config.Tunnel {
	var out []config.Tunnel
	for _, t := range visible(st) {
		if _, ok := st.Tunnel(t.Name); ok {
			out = append(out, t)
		}
	}
	return out
}

func (s *session) forwards() {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		var items []item
		for _, f := range st.Forwards {
			to := ""
			if f.To != 0 {
				to = fmt.Sprintf(" → port %d", f.To)
			}
			items = append(items, item{f.Ports, fmt.Sprintf("%s via %s%s", f.Proto, f.Tunnel, to), false})
		}
		items = append(items, item{"+ Forward ports", "", false})
		sel = s.choose("Port forwarding · ports of this server → tunnel → other side", items, sel)
		if s.quit {
			return
		}
		switch {
		case sel < 0:
			return
		case sel == len(st.Forwards):
			local := localTunnels(st)
			if len(local) == 0 {
				s.page("Port forwarding", func() error { return fmt.Errorf("create a tunnel first") })
				continue
			}
			items := make([]item, len(local))
			for i, t := range local {
				items[i] = tunnelItem(st, t)
			}
			if c := s.choose("Forward ports · through which tunnel?", items, 0); c >= 0 {
				s.addForward(local[c].Name)
			}
		default:
			f := st.Forwards[sel]
			if s.t.confirm("Remove the forward of "+bold(f.Ports)+" via "+f.Tunnel+"?", false) {
				s.page("Removing forward", func() error {
					ch, err := manage.RemoveForward(f.Tunnel, f.Ports)
					if err != nil {
						return err
					}
					printChange(ch)
					success("removed")
					return nil
				})
			}
		}
	}
}

func (s *session) addForward(tunnel string) {
	s.form("Forward ports through "+tunnel, "")
	ports, ok := s.t.input("Ports of this server (e.g. 443,2053-2083)", "", func(v string) error {
		_, err := forward.ParsePorts(v)
		return err
	})
	if !ok {
		return
	}
	to, ok := s.t.input("Target port on the other side (empty = same port)", "", validPort)
	if !ok {
		return
	}
	proto, ok := chooseProto(s, "Forward "+ports+" · protocol")
	if !ok {
		return
	}
	n, _ := strconv.Atoi(to)
	s.page("Forward ports", func() error {
		ch, err := manage.AddForward(manage.ForwardRequest{Tunnel: tunnel, Ports: ports, Proto: proto, To: uint16(n)})
		if err != nil {
			return err
		}
		printChange(ch)
		success("ports %s (%s) now go through %s", ports, proto, tunnel)
		return nil
	})
}

// ---------- speed test ----------

func (s *session) speed() {
	sel := 0
	for {
		sel = s.choose("Speed test", []item{
			{"Test a tunnel", "ping, jitter, loss, download and upload inside it", false},
			{"Compare every tunnel kind with a server", "builds temporary tunnels, measures, removes them", false},
		}, sel)
		st, err := loadNode()
		if err != nil {
			return
		}
		switch sel {
		case 0:
			local := localTunnels(st)
			if len(local) == 0 {
				s.page("Speed test", func() error { return fmt.Errorf("no tunnels created on this server yet") })
				continue
			}
			items := make([]item, len(local))
			for i, t := range local {
				items[i] = tunnelItem(st, t)
			}
			if c := s.choose("Test which tunnel?", items, 0); c >= 0 {
				name := local[c].Name
				s.page("Speed test · "+name, func() error { return runTest(name, bench.DefaultOptions()) })
			}
		case 1:
			var ps []config.Peer
			for _, p := range st.Peers {
				if p.Paired() {
					ps = append(ps, p)
				}
			}
			if len(ps) == 0 {
				s.page("Compare tunnel kinds", func() error { return fmt.Errorf("no connected destination server yet") })
				continue
			}
			items := make([]item, len(ps))
			for i, p := range ps {
				items[i] = item{shown(p.Label, p.Name), p.Address, false}
			}
			if c := s.choose("Compare with which server?", items, 0); c >= 0 {
				name := ps[c].Name
				s.page("Compare tunnel speeds · "+name, func() error { return runBench(name, bench.DefaultOptions(), nil) })
			}
		default:
			return
		}
	}
}

// ---------- join a main server ----------

func (s *session) join() {
	s.form("Join a main server", "   On the main server open Destination servers → Connect command (menu or\n   panel), then paste the setup key that starts with NT1. here.")
	key, ok := s.t.input("Setup key", "", func(v string) error {
		_, err := setuplink.Decode(v)
		return err
	})
	if !ok {
		return
	}
	s.page("Joining the main server", func() error { return applyKey(key, false) })
}

// ---------- web panel ----------

func (s *session) panel() {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		on := st.Panel != nil
		items := []item{
			{"Show address and username", "", !on},
			{"Reset password", "a new random password", !on},
			{"Change port", "", !on},
			{"Turn the panel off", "", !on},
			{"Turn the panel on", "random free port and password", on},
		}
		sel = s.choose("Web panel", items, sel)
		if s.quit {
			return
		}
		switch sel {
		case 0:
			s.page("Web panel", func() error {
				printCredentials(web.Credentials{URL: web.PanelURL(st), User: st.Panel.User})
				return nil
			})
		case 1:
			if s.t.confirm("Reset the panel password?", false) {
				s.page("Web panel", func() error {
					c, err := web.Setup(0, "", "")
					if err == nil {
						printCredentials(c)
					}
					return err
				})
			}
		case 2:
			s.form("Change the panel port", "")
			p, ok := s.t.input("New port", "", func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || !web.PortFree(n) {
					return fmt.Errorf("enter a free port")
				}
				return nil
			})
			if ok {
				s.page("Web panel", func() error { return run("panel", "port", p) })
			}
		case 3:
			if s.t.confirm("Turn the web panel off?", false) {
				s.page("Web panel", func() error { return run("panel", "disable") })
			}
		case 4:
			s.page("Web panel", func() error { return run("panel", "setup") })
		default:
			return
		}
	}
}

// ---------- system ----------

func (s *session) system() {
	sel := 0
	for {
		st, err := loadNode()
		if err != nil {
			return
		}
		items := []item{
			{"Server name and public IP", st.Self + " · " + st.Address, false},
			{"Network boost", "BBR, fq and larger buffers (persistent)", false},
			{"Re-apply all tunnels now", "", false},
			{"Restart the service", "", false},
			{"Update NeoTunnel", "download and install the latest release", false},
			{"Uninstall", "remove tunnels, service, settings and the program", false},
		}
		sel = s.choose("System", items, sel)
		if s.quit {
			return
		}
		switch sel {
		case 0:
			s.form("Server name and public IP", "")
			name, ok := s.t.input("Name", st.Self, manage.ValidNodeName)
			if !ok {
				continue
			}
			ip, ok := s.t.input("Public IP", st.Address, validIP)
			if !ok {
				continue
			}
			s.page("Server name and public IP", func() error {
				if err := manage.SetNode(name, ip); err != nil {
					return err
				}
				success("saved")
				return nil
			})
		case 1:
			s.page("Network boost", func() error {
				if err := sysctl.ApplyBoost(); err != nil {
					return err
				}
				success("BBR, fq and buffer tuning applied and saved")
				return nil
			})
		case 2:
			s.page("Re-apply tunnels", func() error { return report(engine.Apply(st)) })
		case 3:
			s.page("Restart the service", func() error {
				if err := service.Install(); err != nil {
					return err
				}
				success("service restarted")
				return nil
			})
		case 4:
			s.update()
		case 5:
			if s.uninstall() {
				return
			}
		default:
			return
		}
	}
}
