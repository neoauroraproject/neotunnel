// Package clients reports who uses a forwarded port: the client addresses
// with connections open now (from the kernel's connection tracking, with
// their bytes when it counts them) and every address that opened one in the
// last day (from the record forward.SeenScript keeps in the kernel). It
// reads both only when asked; nothing runs in the background.
package clients

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Client is one address using a port.
type Client struct {
	IP string `json:"ip"`
	// Conns are its connections open now; Rx and Tx their bytes so far, to
	// the client and from it (0 while the kernel doesn't count them), and
	// Since when the oldest of them opened (unix seconds, 0 = unknown).
	Conns int    `json:"conns"`
	Rx    uint64 `json:"rx"`
	Tx    uint64 `json:"tx"`
	Since int64  `json:"since,omitempty"`
	// Last is when it last opened a connection, within the last day (0 =
	// not recorded).
	Last int64 `json:"last,omitempty"`
	// IR says the address is in Iran (by the firewall guard's list).
	IR bool `json:"ir"`
}

// Report is a port's clients, busiest first.
type Report struct {
	Clients []Client `json:"clients"`
	// Conns is all open connections; Day the addresses of the last day.
	Conns int `json:"conns"`
	Day   int `json:"day"`
	// Bytes says whether the kernel counts each connection's bytes, Times
	// whether it records when each opened (both on from the first report).
	Bytes bool `json:"bytes"`
	Times bool `json:"times"`
}

// Port is a forward's ports, by protocol, as the report matches them.
type Port struct {
	Proto []string // tcp and/or udp
	Ports []int
}

func (p Port) has(proto string, port int) bool {
	return slices.Contains(p.Proto, proto) && slices.Contains(p.Ports, port)
}

// flow is one open connection to this server.
type flow struct {
	proto  string
	client netip.Addr
	port   int
	rx, tx uint64
	start  int64
}

// seen is one entry of the day's record.
type seen struct {
	client netip.Addr
	proto  string
	port   int
	last   int64
}

// build groups the flows and the day's record of a port by address.
func build(p Port, flows []flow, day []seen) Report {
	by := map[netip.Addr]*Client{}
	get := func(a netip.Addr) *Client {
		c := by[a]
		if c == nil {
			c = &Client{IP: a.String()}
			by[a] = c
		}
		return c
	}
	var r Report
	for _, f := range flows {
		if !p.has(f.proto, f.port) {
			continue
		}
		c := get(f.client)
		c.Conns++
		c.Rx += f.rx
		c.Tx += f.tx
		if f.start > 0 && (c.Since == 0 || f.start < c.Since) {
			c.Since = f.start
		}
		r.Conns++
	}
	days := map[netip.Addr]bool{}
	for _, s := range day {
		if !p.has(s.proto, s.port) {
			continue
		}
		days[s.client] = true
		if c := get(s.client); s.last > c.Last {
			c.Last = s.last
		}
	}
	r.Day = len(days)
	for _, c := range by {
		r.Clients = append(r.Clients, *c)
	}
	slices.SortFunc(r.Clients, func(a, b Client) int {
		switch {
		case a.Rx+a.Tx != b.Rx+b.Tx:
			return cmp(b.Rx+b.Tx, a.Rx+a.Tx)
		case a.Conns != b.Conns:
			return b.Conns - a.Conns
		case a.Last != b.Last:
			return cmp(b.Last, a.Last)
		}
		return strings.Compare(a.IP, b.IP)
	})
	return r
}

func cmp[T uint64 | int64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// PortOf turns a forward's protocol ("tcp", "udp", "both") and ports
// ("443, 2053-2056") into what the report matches.
func PortOf(proto, ports string, expand func(string) []int) Port {
	p := Port{Ports: expand(ports)}
	switch proto {
	case "udp":
		p.Proto = []string{"udp"}
	case "both":
		p.Proto = []string{"tcp", "udp"}
	default:
		p.Proto = []string{"tcp"}
	}
	return p
}

// protoName names an IP protocol number the way nft does.
func protoName(n int) string {
	switch n {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	}
	return strconv.Itoa(n)
}

// parseSeen reads a set listing: each element is address . protocol .
// port, with how long it has left of its day.
func parseSeen(b []byte, now int64) []seen {
	var doc struct {
		Nftables []struct {
			Set *struct {
				Timeout int64             `json:"timeout"`
				Elem    []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []seen
	for _, o := range doc.Nftables {
		if o.Set == nil {
			continue
		}
		for _, raw := range o.Set.Elem {
			var e struct {
				Elem struct {
					Val struct {
						Concat []json.RawMessage `json:"concat"`
					} `json:"val"`
					Timeout int64 `json:"timeout"`
					Expires int64 `json:"expires"`
				} `json:"elem"`
			}
			if json.Unmarshal(raw, &e) != nil || len(e.Elem.Val.Concat) != 3 {
				continue
			}
			var ip, proto string
			var port int
			json.Unmarshal(e.Elem.Val.Concat[0], &ip)
			json.Unmarshal(e.Elem.Val.Concat[1], &proto)
			json.Unmarshal(e.Elem.Val.Concat[2], &port)
			a, err := netip.ParseAddr(ip)
			if err != nil {
				continue
			}
			timeout := e.Elem.Timeout
			if timeout == 0 {
				timeout = o.Set.Timeout
			}
			s := seen{client: a.Unmap(), proto: proto, port: port}
			if timeout > 0 && e.Elem.Expires > 0 {
				s.last = now - (timeout - e.Elem.Expires)
			}
			out = append(out, s)
		}
	}
	return out
}
