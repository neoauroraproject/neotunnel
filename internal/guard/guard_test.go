package guard

import (
	"net/netip"
	"strings"
	"testing"

	"neotunnel/internal/config"
)

func TestClassify(t *testing.T) {
	forwards := []Socket{{Proto: "tcp", Port: 10400}, {Proto: "udp", Port: 10400}}
	sockets := []Socket{
		{Proto: "tcp", Port: 31234, Process: "xray-linux-amd64"}, // a 3x-ui inbound
		{Proto: "tcp", Port: 2053, Process: "x-ui"},              // 3x-ui's own panel
		{Proto: "tcp", Port: 80, Process: "nginx"},
		{Proto: "tcp", Port: 41485, Process: "neotunnel"}, // agent: peers only
		{Proto: "udp", Port: 4789},                        // kernel (VXLAN)
		{Proto: "tcp", Port: 22, Process: "sshd"},
		{Proto: "tcp", Port: 10400, Process: "neotunnel"}, // a relay forward's listener
	}
	always := map[string]bool{"tcp/22": true, "tcp/2096": true}

	open := Classify(config.Guard{}, forwards, sockets, always)
	got := map[string]string{}
	for _, p := range open {
		got[p.Key()] = p.Scope
	}
	want := map[string]string{"tcp/80": "all", "tcp/2053": "all", "tcp/10400": "all", "udp/10400": "all", "tcp/31234": "all"}
	if len(got) != len(want) {
		t.Fatalf("ports %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}

	g := config.Guard{ConfigIran: true, Ports: map[string]string{"tcp/80": "closed", "tcp/2053": "iran", "tcp/31234": "all"}}
	got = map[string]string{}
	for _, p := range Classify(g, forwards, sockets, always) {
		got[p.Key()] = p.Scope
	}
	for k, v := range map[string]string{"tcp/10400": "iran", "udp/10400": "iran", "tcp/31234": "all", "tcp/2053": "iran", "tcp/80": "closed"} {
		if got[k] != v {
			t.Errorf("config ports from Iran, %s: %q, want %q", k, got[k], v)
		}
	}
}

func TestScriptKeepsSSHAndPanelFirst(t *testing.T) {
	ir := []netip.Prefix{netip.MustParsePrefix("2.144.0.0/14"), netip.MustParsePrefix("2a01:5ec0::/29")}
	s := Script(Input{
		Ifaces: []string{"ens192"}, Peers: []netip.Addr{netip.MustParseAddr("203.0.113.9")},
		SSH: []uint16{22, 2222}, Panel: 34127, ICMP: "iran", IR: ir,
		Ports: []Port{{Proto: "tcp", Port: 10400, Scope: "iran"}, {Proto: "tcp", Port: 80, Scope: "all"}, {Proto: "tcp", Port: 8080, Scope: "closed"}},
		Block: []string{"0.0.0.0/0", "geoip:ir"}, Allow: []string{"198.51.100.7"},
	})
	in := s[strings.Index(s, "chain input"):strings.Index(s, "chain forward")]
	ssh, block := strings.Index(in, "tcp dport { 22, 2222, 34127 } accept"), strings.Index(in, "@block4 drop")
	if ssh < 0 || block < 0 || ssh > block {
		t.Fatalf("SSH and the panel must be accepted before the block list:\n%s", in)
	}
	for _, want := range []string{
		`iifname != { "ens192" } accept`,
		"ip saddr @peers4 accept",
		"elements = { 203.0.113.9 }",
		"elements = { tcp . 80 }",
		"elements = { tcp . 10400 }",
		"elements = { 2.144.0.0/14 }",
		"elements = { 2a01:5ec0::/29 }",
		"elements = { 198.51.100.7/32 }",
		"elements = { 0.0.0.0/0, 2.144.0.0/14 }",
		"icmp type echo-request ip saddr @ir4 accept",
		"packet-too-big",
		"meta l4proto . ct original proto-dst @open_iran ip saddr @ir4 accept",
		"counter name dropped drop",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "8080") {
		t.Error("a closed port is open")
	}
	if strings.Contains(Script(Input{ICMP: "closed", Ifaces: []string{"e"}}), "echo-request") {
		t.Error("ping answered while closed")
	}
	off := Script(Input{})
	if strings.Contains(off, "chain") || !strings.Contains(off, "delete table inet "+Table) {
		t.Errorf("off should only remove the table:\n%s", off)
	}
}

func TestParseProcNet(t *testing.T) {
	text := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 222 1 0000000000000000 100 0 0 10 0
   2: 0A00000A:0016 0B00000A:C350 01 00000000:00000000 00:00000000 00000000     0        0 333 1 0000000000000000 100 0 0 10 0
`
	got := ParseProcNet(text)
	if len(got) != 3 {
		t.Fatalf("%d sockets", len(got))
	}
	if got[0].Local != netip.MustParseAddrPort("0.0.0.0:22") || got[0].State != StateListen || got[0].Inode != 12345 {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Local.Addr() != netip.MustParseAddr("127.0.0.1") || Reachable(got[1].Local.Addr()) {
		t.Errorf("loopback: %+v", got[1])
	}
	if got[2].Local != netip.MustParseAddrPort("10.0.0.10:22") || got[2].State != StateEstablished {
		t.Errorf("session: %+v", got[2])
	}
	v6 := `  sl  local_address                         remote_address                        st
   0: 00000000000000000000000001000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 777 1
`
	if g := ParseProcNet(v6); len(g) != 1 || g[0].Local != netip.MustParseAddrPort("[::1]:22") {
		t.Errorf("v6: %+v", g)
	}
}

func TestEntriesAndList(t *testing.T) {
	for in, ok := range map[string]bool{"1.2.3.4": true, "1.2.3.0/24": true, "2001:db8::/32": true, "geoip:ir": true, "GEOIP:IR": true, "geoip:us": false, "example.com": false} {
		if _, _, err := ParseEntry(in); (err == nil) != ok {
			t.Errorf("ParseEntry(%q): %v", in, err)
		}
	}
	if p, _, _ := ParseEntry("1.2.3.4"); p.String() != "1.2.3.4/32" {
		t.Errorf("single address: %s", p)
	}
	ps := ParseList(bundledIR)
	v6 := 0
	for _, p := range ps {
		if p.Addr().Is6() {
			v6++
		}
	}
	if len(ps) < minRanges || v6 == 0 {
		t.Fatalf("bundled list: %d ranges, %d IPv6", len(ps), v6)
	}
}
