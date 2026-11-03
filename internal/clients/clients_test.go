package clients

import (
	"net/netip"
	"testing"
)

// nft 1.0.9's listing of the day's record.
const listing = `{"nftables": [{"metainfo": {"version": "1.0.9", "release_name": "Old Doc Yak #3", "json_schema_version": 1}}, {"set": {"family": "inet", "name": "c4", "table": "neotunnel_seen", "type": ["ipv4_addr", "inet_proto", "inet_service"], "handle": 1, "size": 262144, "flags": ["timeout", "dynamic"], "timeout": 86400, "elem": [{"elem": {"val": {"concat": ["198.18.0.2", "tcp", 8095]}, "expires": 82799}}, {"elem": {"val": {"concat": ["198.18.0.3", "udp", 443]}, "expires": 86400}}]}}]}`

func TestParseSeen(t *testing.T) {
	got := parseSeen([]byte(listing), 1_000_000)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if s := got[0]; s.client != netip.MustParseAddr("198.18.0.2") || s.proto != "tcp" || s.port != 8095 || s.last != 1_000_000-3601 {
		t.Errorf("first %+v", s)
	}
	if s := got[1]; s.proto != "udp" || s.port != 443 || s.last != 1_000_000 {
		t.Errorf("second %+v", s)
	}
}

// Connections and the day's record group by address, the busiest first;
// other ports and protocols stay out.
func TestBuild(t *testing.T) {
	a, b, c := netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("2001:db8::5")
	p := PortOf("both", "443, 2053-2054", func(string) []int { return []int{443, 2053, 2054} })
	flows := []flow{
		{proto: "tcp", client: a, port: 443, rx: 1000, tx: 100, start: 50},
		{proto: "tcp", client: a, port: 2053, rx: 500, tx: 50, start: 40},
		{proto: "udp", client: b, port: 443, rx: 9000, tx: 900, start: 70},
		{proto: "tcp", client: c, port: 8443, rx: 1 << 30},    // another port
		{proto: "udp", client: c, port: 2055, rx: 1 << 30},    // out of the range
		{proto: "tcp", client: b, port: 443, rx: 0, start: 0}, // bytes not counted yet
	}
	day := []seen{{client: a, proto: "tcp", port: 443, last: 90}, {client: c, proto: "udp", port: 2054, last: 80}, {client: c, proto: "tcp", port: 9999, last: 99}}
	r := build(p, flows, day)
	if r.Conns != 4 || r.Day != 2 || len(r.Clients) != 3 {
		t.Fatalf("%+v", r)
	}
	if x := r.Clients[0]; x.IP != b.String() || x.Conns != 2 || x.Rx != 9000 || x.Since != 70 {
		t.Errorf("first %+v", x)
	}
	if x := r.Clients[1]; x.IP != a.String() || x.Conns != 2 || x.Rx != 1500 || x.Tx != 150 || x.Since != 40 || x.Last != 90 {
		t.Errorf("second %+v", x)
	}
	if x := r.Clients[2]; x.IP != c.String() || x.Conns != 0 || x.Last != 80 {
		t.Errorf("third %+v", x)
	}
}
