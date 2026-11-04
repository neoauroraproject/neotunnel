// Package forward renders port forwarding and MSS clamping as one nftables
// table ("inet neotunnel") that is replaced atomically on every apply.
package forward

import (
	"fmt"
	"hash/fnv"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"neotunnel/internal/config"
	"neotunnel/internal/relay"
)

// Reserved ports are never forwarded so a typo can't lock you out.
var Reserved = map[int]string{22: "SSH", 7710: "neotunnel agent", 7711: "neotunnel probe", relay.RelayPort: "neotunnel relay", config.FetchPort: "neotunnel downloads"}

type Rule struct {
	Iface string
	// Lanes are the tunnel's other interfaces when it runs on several lanes.
	Lanes  []string
	Proto  []string // tcp and/or udp
	Ports  string   // nft set body: "443, 8080-8090"
	Target netip.Addr
	To     uint16 // target port; 0 = keep the original port
	// Transparent forwards keep the client's address (no masquerade).
	Transparent bool
	// Mark routes the forward to Target, the far server's public address,
	// through the tunnel (see Public); 0 = Target is inside the tunnel.
	Mark uint32
	// Key identifies the forward for its traffic counters ("tcp/443").
	Key string
}

// dnat is the rule's address family as nft names it in meta nfproto and in
// dnat, and its target ("10.77.0.2", "[fd00::2]:443").
func (r Rule) dnat() (fam, nat, target string) {
	fam, nat, target = "ipv4", "ip", r.Target.String()
	if r.Target.Is6() {
		fam, nat, target = "ipv6", "ip6", "["+target+"]"
	}
	if r.To != 0 {
		target += ":" + strconv.Itoa(int(r.To))
	}
	return fam, nat, target
}

// localnetIfaces are the tunnel interfaces that IPv4 forwards leave on:
// connections this server makes to 127.0.0.1:port go out on them, and
// their replies, addressed to 127.0.0.1 once un-NATed, come back on them,
// which the kernel only accepts with route_localnet set there.
func localnetIfaces(rs Ruleset) map[string]bool {
	out := map[string]bool{}
	for _, r := range rs.Rules {
		if r.Target.Is4() {
			out[r.Iface] = true
			for _, l := range r.Lanes {
				out[l] = true
			}
		}
	}
	return out
}

func localnet(rs Ruleset) bool { return len(localnetIfaces(rs)) > 0 }

type Ruleset struct {
	Rules  []Rule
	Ifaces []string // tunnel interfaces that get MSS clamping
	// Relays are proxy-mode forwards, served by the daemon instead of nft.
	Relays []relay.Spec
	// Exits are tunnels whose relay-mode forwards end on this node.
	Exits []relay.Exit
	// Returns are tunnels whose far side forwards clients transparently:
	// connections arriving on them are marked and their replies routed back.
	Returns []Return
	// Protect lists encrypted tunnels, whose packets are dropped when they
	// arrive without IPsec.
	Protect []Protect
	// Carriers are the outer packets of the kernel tunnels, which connection
	// tracking skips (see CarrierScript).
	Carriers []Protect
	// Fetch redirects this server's own downloads (nil = off).
	Fetch *FetchRedirect
	// Accounts are the forwards whose clients' traffic is counted, in every
	// mode, Plans the sold ones' limits (see config.PortPlan).
	Accounts []Account
	Plans    []Plan
	// Publics are the tunnels that carry forwards to their far server's
	// public address.
	Publics []Public
	// Inner are the subnets inside this node's tunnels. Packets for them
	// never leave by any other interface: with a tunnel down they would take
	// the default route to the datacenter's network, which drops or reports
	// traffic to private addresses.
	Inner []netip.Prefix
}

// Account is a forward's ports, counted under Key ("tcp/443").
type Account struct {
	Key   string
	Proto []string
	Ports string // nft set body
}

// Plan is what the kernel enforces of a sold port: a speed cap each way,
// open connections, client addresses, or the port closed.
type Plan struct {
	Account
	RateMbit int
	MaxConns int
	MaxIPs   int
	// Closed is why the port takes no traffic ("" = open).
	Closed string
}

// Why a sold port is closed.
const (
	ClosedPaused  = "paused"
	ClosedExpired = "expired"
	ClosedQuota   = "quota"
)

var (
	closedMu sync.Mutex
	closed   = map[string]string{}
)

// SetClosed sets the sold ports that are closed now, by forward key, with
// why; the daemon decides it from the plans, the clock and the traffic.
func SetClosed(m map[string]string) {
	closedMu.Lock()
	defer closedMu.Unlock()
	closed = m
}

// Closed says why a forward's port is closed ("" = open).
func Closed(key string) string {
	closedMu.Lock()
	defer closedMu.Unlock()
	return closed[key]
}

// FetchRedirect is the downloads redirect: All takes every program's
// connections, otherwise only those of terminal and SSH sessions.
type FetchRedirect struct {
	All bool
}

// Return is a tunnel that carries transparent forwards from its far side.
type Return struct {
	Iface string
	Lanes []string // the tunnel's other lane interfaces
	Mark  uint32
}

// Protect matches the outer packets of an encrypted tunnel.
type Protect struct {
	Remote netip.Addr
	Proto  int    // outer IP protocol
	Port   uint16 // UDP destination port, for UDP-based tunnels
}

// transparentMark tags connections forwarded transparently on this node so
// the postrouting chain leaves their source address alone.
const transparentMark = 0x4e550001

// ReturnMark is the connection and routing mark of a tunnel's return path
// (the engine uses it for the policy routing rule as well).
func ReturnMark(tunnel string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(tunnel))
	return 0x4e540000 | h.Sum32()&0xffff
}

// Public is a tunnel whose forwards go to its far server's public address.
// The tunnel's own outer packets go to that same address, so it can't be
// routed through the tunnel as a whole: the forwards' connections are
// marked, and only marked packets take the tunnel (see the engine).
type Public struct {
	Iface string
	Lanes []string
	Mark  uint32
	Dest  netip.Addr // the far server's public address
	Via   netip.Addr // its address inside the tunnel
}

// PublicMark is the connection and routing mark of the forwards a tunnel
// delivers at its far server's public address.
func PublicMark(tunnel string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(tunnel))
	return publicMarks | h.Sum32()&0xffff
}

// publicMarks are the top 16 bits of every PublicMark.
const publicMarks = 0x4e560000

// IsPublicMark reports whether m is a PublicMark.
func IsPublicMark(m uint32) bool { return m&0xffff0000 == publicMarks }

// MaxRelayPorts caps how many ports one proxy-mode forward may listen on.
const MaxRelayPorts = 2000

// ParsePorts validates "443,8080-8090" and returns the nft set body.
func ParsePorts(s string) (string, error) {
	var parts []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(f, "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return "", fmt.Errorf("bad port %q", f)
		}
		for p, name := range Reserved {
			if p >= a && p <= b {
				return "", fmt.Errorf("port %d is reserved for %s", p, name)
			}
		}
		if isRange {
			parts = append(parts, fmt.Sprintf("%d-%d", a, b))
		} else {
			parts = append(parts, strconv.Itoa(a))
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no ports given")
	}
	return strings.Join(parts, ", "), nil
}

// ValidateTo checks a forward's target port.
func ValidateTo(to int) error {
	if to < 0 || to > 65535 {
		return fmt.Errorf("bad target port %d", to)
	}
	return nil
}

func protos(p string) ([]string, error) {
	switch p {
	case "", "tcp":
		return []string{"tcp"}, nil
	case "udp":
		return []string{"udp"}, nil
	case "both":
		return []string{"tcp", "udp"}, nil
	}
	return nil, fmt.Errorf("bad protocol %q (tcp, udp, both)", p)
}

// ifMatch is the nft operand that matches a tunnel's interfaces: its own
// name, or a set when it runs on several lanes.
func ifMatch(iface string, lanes []string) string {
	if len(lanes) == 0 {
		return strconv.Quote(iface)
	}
	q := []string{strconv.Quote(iface)}
	for _, l := range lanes {
		q = append(q, strconv.Quote(l))
	}
	return "{ " + strings.Join(q, ", ") + " }"
}
