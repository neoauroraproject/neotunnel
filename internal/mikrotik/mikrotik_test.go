package mikrotik

import (
	"strings"
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/engine"
)

const (
	server = "203.0.113.1"
	router = "198.51.100.7"
	secret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
)

func remote(name string, kind config.Kind) config.Tunnel {
	return config.Tunnel{Name: name, Kind: kind,
		Sides: [2]config.Side{{Node: "ir1", Endpoint: server}, {Endpoint: router}},
		Net4:  "10.77.0.4/30", Key: 0x12345678, Secret: secret}
}

func u16(v uint16) *uint16 { return &v }

func TestCommands(t *testing.T) {
	sit := remote("mk-6", config.KindSIT)
	sit.Net4, sit.Net6 = "", "fd00:1:2:3::/64"
	ipip6 := remote("mk", config.KindIPIP6)
	ipip6.Over, ipip6.Mode, ipip6.Sides[0].Endpoint, ipip6.Sides[1].Endpoint = "mk-6", "6to4", "", ""

	eoip := remote("eo", config.KindEoIP)
	eoip.TunnelID = u16(42)
	eoip6 := remote("eo6", config.KindEoIP)
	eoip6.Sides[0].Endpoint, eoip6.Sides[1].Endpoint = "2001:db8::1", "2001:db8::2"

	vx := remote("vx", config.KindVXLAN)
	vx.VNI = 4242
	l2 := remote("l2", config.KindL2TPv3)
	l2u := remote("l2u", config.KindL2TPv3)
	l2u.Port = 1701

	wg := remote("wg", config.KindWireGuard)
	wg.Port, wg.Net6 = 51821, "fd00:1:2:4::/64"
	wg.PeerKey = "cm91dGVyLXB1YmxpYy1rZXktMzItYnl0ZXMtbG9uZyE="
	wgOff := remote("wgk", config.KindWireGuard)
	wgOff.Port, wgOff.Keepalive = 51822, -1

	gre := remote("gre", config.KindGRE)
	gre.DSCP = 46

	tests := []struct {
		name    string
		tunnels []config.Tunnel
		forward []config.Forward
		want    string
	}{
		{"gre", []config.Tunnel{gre}, nil, `# NeoTunnel gre (gre): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface gre add name=nt-gre remote-address=203.0.113.1 mtu=1476 dscp=46 comment="NeoTunnel gre"
/ip address add address=10.77.0.6/30 interface=nt-gre comment="NeoTunnel gre"
`},
		{"ipip with forwards", []config.Tunnel{remote("ip", config.KindIPIP)}, []config.Forward{
			{Node: "ir1", Tunnel: "ip", Proto: "tcp", Ports: "443,8080-8090"},
			{Node: "ir1", Tunnel: "ip", Proto: "both", Ports: "2053", To: 53, Mode: config.ForwardProxy},
		}, `# NeoTunnel ip (ipip): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface ipip add name=nt-ip remote-address=203.0.113.1 mtu=1480 comment="NeoTunnel ip"
/ip address add address=10.77.0.6/30 interface=nt-ip comment="NeoTunnel ip"
# Ports forwarded here arrive on 10.77.0.6 from 10.77.0.5 (connected, no route needed):
#   tcp 443,8080-8090 -> same port
#   both 2053 -> port 53, with a PROXY protocol v2 header
# to pass them on to a host behind the router, e.g.:
# /ip firewall nat add chain=dstnat in-interface=nt-ip protocol=tcp dst-port=443,8080-8090 action=dst-nat to-addresses=192.168.88.10
`},
		{"6to4 stack", []config.Tunnel{sit, ipip6}, nil, `# NeoTunnel mk (6to4): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface 6to4 add name=nt-mk-6 remote-address=203.0.113.1 mtu=1480 comment="NeoTunnel mk-6"
/interface ipipv6 add name=nt-mk remote-address=fd00:1:2:3::1 local-address=fd00:1:2:3::2 mtu=1440 comment="NeoTunnel mk"
/ipv6 address add address=fd00:1:2:3::2/64 interface=nt-mk-6 advertise=no comment="NeoTunnel mk-6"
/ip address add address=10.77.0.6/30 interface=nt-mk comment="NeoTunnel mk"
`},
		{"eoip", []config.Tunnel{eoip}, nil, `# NeoTunnel eo (eoip): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface eoip add name=nt-eo remote-address=203.0.113.1 tunnel-id=42 mtu=1458 comment="NeoTunnel eo"
/ip address add address=10.77.0.6/30 interface=nt-eo comment="NeoTunnel eo"
`},
		{"eoipv6", []config.Tunnel{eoip6}, nil, `# NeoTunnel eo6 (eoip): RouterOS v7 commands for the router at 2001:db8::2
# NeoTunnel server: 2001:db8::1
/interface eoipv6 add name=nt-eo6 remote-address=2001:db8::1 tunnel-id=1656 mtu=1444 comment="NeoTunnel eo6"
/ip address add address=10.77.0.6/30 interface=nt-eo6 comment="NeoTunnel eo6"
`},
		{"vxlan", []config.Tunnel{vx}, nil, `# NeoTunnel vx (vxlan): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface vxlan add name=nt-vx vni=4242 port=4789 mtu=1450 comment="NeoTunnel vx"
/interface vxlan vteps add interface=nt-vx remote-ip=203.0.113.1
/ip address add address=10.77.0.6/30 interface=nt-vx comment="NeoTunnel vx"
`},
		{"l2tpv3 ip", []config.Tunnel{l2}, nil, `# NeoTunnel l2 (l2tpv3): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
# local-address is the router's own address; behind NAT, use the address on its WAN interface
/interface l2tp-ether add name=nt-l2 connect-to=203.0.113.1 local-address=198.51.100.7 unmanaged-mode=yes l2tp-proto-version=l2tpv3-ip local-tunnel-id=305419896 remote-tunnel-id=305419896 local-session-id=305419896 remote-session-id=305419896 mtu=1462 disabled=no comment="NeoTunnel l2"
/ip address add address=10.77.0.6/30 interface=nt-l2 comment="NeoTunnel l2"
`},
		{"l2tpv3 udp", []config.Tunnel{l2u}, nil, `# NeoTunnel l2u (l2tpv3): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
# local-address is the router's own address; behind NAT, use the address on its WAN interface
/interface l2tp-ether add name=nt-l2u connect-to=203.0.113.1 local-address=198.51.100.7 unmanaged-mode=yes l2tp-proto-version=l2tpv3-udp local-tunnel-id=305419896 remote-tunnel-id=305419896 local-session-id=305419896 remote-session-id=305419896 mtu=1450 disabled=no comment="NeoTunnel l2u"
/ip address add address=10.77.0.6/30 interface=nt-l2u comment="NeoTunnel l2u"
`},
		{"wireguard", []config.Tunnel{wg}, nil, `# NeoTunnel wg (wireguard): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface wireguard add name=nt-wg listen-port=51821 mtu=1440 comment="NeoTunnel wg"
/interface wireguard peers add interface=nt-wg public-key="WG_PUB" endpoint-address=203.0.113.1 endpoint-port=51821 allowed-address=10.77.0.5/32,fd00:1:2:4::1/128 persistent-keepalive=25s comment="NeoTunnel wg"
# NeoTunnel expects the router's public key cm91dGVyLXB1YmxpYy1rZXktMzItYnl0ZXMtbG9uZyE=;
# if the router made a different one, give NeoTunnel this key as the peer key:
:put [/interface wireguard get nt-wg public-key]
/ip address add address=10.77.0.6/30 interface=nt-wg comment="NeoTunnel wg"
/ipv6 address add address=fd00:1:2:4::2/64 interface=nt-wg advertise=no comment="NeoTunnel wg"
`},
		{"wireguard no keepalive", []config.Tunnel{wgOff}, nil, `# NeoTunnel wgk (wireguard): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface wireguard add name=nt-wgk listen-port=51822 mtu=1440 comment="NeoTunnel wgk"
/interface wireguard peers add interface=nt-wgk public-key="WG_PUB" endpoint-address=203.0.113.1 endpoint-port=51822 allowed-address=10.77.0.5/32 comment="NeoTunnel wgk"
# give NeoTunnel the router's public key (peer key):
:put [/interface wireguard get nt-wgk public-key]
/ip address add address=10.77.0.6/30 interface=nt-wgk comment="NeoTunnel wgk"
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &config.State{Self: "ir1", Tunnels: tc.tunnels, Forwards: tc.forward}
			top := tc.tunnels[len(tc.tunnels)-1]
			got, err := Commands(st, top.Name)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if top.Kind == config.KindWireGuard {
				want = strings.ReplaceAll(want, "WG_PUB", wgPub(t, top))
			}
			if got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestCommandsRefuses(t *testing.T) {
	peer := remote("pe", config.KindGRE)
	peer.Sides[1].Node = "de1"
	fou := remote("fou", config.KindGRE)
	fou.Port = 5555
	eoUDP := remote("eou", config.KindEoIP)
	eoUDP.Port = 5556
	tests := []struct {
		name string
		t    config.Tunnel
		want string
	}{
		{"managed peer", peer, "configures itself"},
		{"fou", fou, "FOU"},
		{"eoip on udp", eoUDP, "only NeoTunnel"},
		{"gretap", remote("gt", config.KindGRETAP), "no GRETAP"},
		{"geneve", remote("gn", config.KindGENEVE), "no geneve"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &config.State{Self: "ir1", Tunnels: []config.Tunnel{tc.t}}
			_, err := Commands(st, tc.t.Name)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if _, err := Commands(&config.State{Self: "ir1"}, "nope"); err == nil {
		t.Fatal("unknown tunnel: no error")
	}
}

func wgPub(t *testing.T, tn config.Tunnel) string {
	t.Helper()
	k, err := engine.WireGuardPublicKey(tn, 0)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
