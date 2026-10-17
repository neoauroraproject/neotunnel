package web

import (
	"context"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
	"neotunnel/internal/traffic"
)

// Backend is everything the panel can read and do. Live implements it on the
// real node; a demo implementation serves the UI with sample data.
type Backend interface {
	Overview() (Overview, error)
	AddPeer(name, address string) (fleet.Setup, error)
	RemovePeer(name string) (manage.Change, error)
	PeerSetup(name string) (fleet.Setup, error)
	SyncPeers(names []string) []manage.PeerSync
	UpgradePeer(ctx context.Context, name string) (string, error)
	CreateTunnel(req manage.TunnelRequest) (manage.Change, error)
	UpdateTunnel(name string, set manage.Settings, port uint16) (manage.Change, error)
	SetLimits(name string, l manage.Limits) (manage.Change, error)
	RemoveTunnel(name string) (manage.Change, error)
	AddForward(req manage.ForwardRequest) (manage.Change, error)
	UpdateForward(req manage.ForwardRequest) (manage.Change, error)
	RemoveForward(tunnel, ports string) (manage.Change, error)
	SetNode(name, address string) error
	SetDefaults(d config.Defaults) error
	TestTunnel(ctx context.Context, name string, progress func(string), live func(string, float64)) ([]bench.Result, error)
	Bench(ctx context.Context, peer string, only []string, progress func(string), live func(string, float64), partial func(bench.Result)) ([]bench.Result, error)
	// Check brings a would-be tunnel up for a few seconds and reports whether
	// traffic passes (all: every kind at once).
	Check(ctx context.Context, req manage.TunnelRequest, all bool) ([]bench.Result, error)
	// ProbeMTU measures the path MTU under a tunnel and remembers it.
	ProbeMTU(ctx context.Context, name string) (MTUReport, error)
	Traffic() map[string]traffic.Summary
	ResetTraffic(tunnel string) error
	System() (SystemInfo, error)
	Boost() error
	Backup() ([]byte, error)

	Login(user, pass string) bool
	ChangePassword(current, next string) error
	SetUser(user string) error
	// SetPanelPort saves a new panel port and returns the new URL; the panel
	// restarts on it right after responding.
	SetPanelPort(port int) (string, error)
}

type Overview struct {
	Node     NodeInfo        `json:"node"`
	Master   *MasterInfo     `json:"master,omitempty"`
	Revision uint64          `json:"revision"`
	Tunnels  []TunnelView    `json:"tunnels"`
	Peers    []PeerView      `json:"peers"`
	Forwards []ForwardView   `json:"forwards"`
	Modes    []manage.Mode   `json:"modes"`
	Ciphers  []config.Cipher `json:"ciphers"`
	Defaults config.Defaults `json:"defaults"`
	Errors   []string        `json:"errors,omitempty"`
	Panel    PanelInfo       `json:"panel"`
	Commands []CommandGroup  `json:"commands"`
	Install  string          `json:"install"` // one-line installer for a new server
	Repo     string          `json:"repo"`
	Time     int64           `json:"time"` // unix ms, for rate calculation
}

type NodeInfo struct {
	Name     string     `json:"name"`
	Address  string     `json:"address"`
	Address6 string     `json:"address6,omitempty"`
	Version  string     `json:"version"`
	Arch     string     `json:"arch"`
	Key      string     `json:"key"`
	Uptime   int64      `json:"uptime_s"`
	Load     [3]float64 `json:"load"`
	CPUs     int        `json:"cpus"`
	MemTotal uint64     `json:"mem_total"`
	MemUsed  uint64     `json:"mem_used"`
	BBR      bool       `json:"bbr"`
	// NIC is the public interface: what the datacenter counts. Rx is the
	// incoming traffic, usually the billed direction.
	NIC      string       `json:"nic,omitempty"`
	NICToday traffic.Pair `json:"nic_today"`
	NICMonth traffic.Pair `json:"nic_month"`
}

type MasterInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Paired  bool   `json:"paired"`
}

type TunnelView struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Mode    string `json:"mode,omitempty"`
	Layer   int    `json:"layer"`
	Over    string `json:"over,omitempty"`
	Carrier bool   `json:"carrier"` // another tunnel runs over this one
	Peer    string `json:"peer,omitempty"`
	FarIP   string `json:"far_ip"`
	Port    uint16 `json:"port,omitempty"`
	Here4   string `json:"here4,omitempty"`
	There4  string `json:"there4,omitempty"`
	Here6   string `json:"here6,omitempty"`
	There6  string `json:"there6,omitempty"`
	MTU     int    `json:"mtu"`
	Up      bool   `json:"up"`
	Rx      uint64 `json:"rx"`
	Tx      uint64 `json:"tx"`
	Managed bool   `json:"managed"` // defined by our master, read-only here
	Error   string `json:"error,omitempty"`

	// Health is the live ping/loss through the tunnel (nil until probed).
	Health *HealthView `json:"health,omitempty"`
	// Stack lists the layers under the tunnel, bottom first (e.g. obfs, sit).
	Stack    []LayerView     `json:"stack,omitempty"`
	Settings manage.Settings `json:"settings"`
	BasePort uint16          `json:"base_port,omitempty"` // UDP port of the bottom layer
	Obfs     bool            `json:"obfs,omitempty"`
	Encrypt  string          `json:"encrypt,omitempty"`  // IPsec or the kind's own encryption
	PathMTU  int             `json:"path_mtu,omitempty"` // measured, when probing
	WGKey    string          `json:"wg_key,omitempty"`   // our WireGuard public key
	VNI      uint32          `json:"vni,omitempty"`
	Total    traffic.Pair    `json:"total"`
	Today    traffic.Pair    `json:"today"`
	Month    traffic.Pair    `json:"month"`
	Tests    traffic.Pair    `json:"tests"`      // speed tests, not in the totals
	Wire     uint64          `json:"wire_today"` // today's bytes on the network, overhead included
	// Limits are the on/off switch, speed limit and monthly quota; Blocked
	// means the quota ran out and the tunnel is down until next month.
	Limits   manage.Limits `json:"limits"`
	Blocked  bool          `json:"blocked,omitempty"`
	Overhead int           `json:"overhead"` // bytes each packet gains on the wire
}

// LayerView is one tunnel of a stack.
type LayerView struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	MTU  int    `json:"mtu"`
	Up   bool   `json:"up"`
}

// MTUReport is the result of a path MTU measurement.
type MTUReport struct {
	Remote   string `json:"remote"`
	PathMTU  int    `json:"path_mtu"`
	Overhead int    `json:"overhead"` // every layer of the stack, encryption included
	Best     int    `json:"best"`     // the MTU the tunnel should have
	Current  int    `json:"current"`
	Probing  bool   `json:"probing"` // the tunnel follows measurements automatically
}

// SystemInfo describes the host's network tuning.
type SystemInfo struct {
	Sysctl  []SysctlView `json:"sysctl"`
	Boosted bool         `json:"boosted"`
	Kernel  string       `json:"kernel"`
	Nft     bool         `json:"nft"`
	Ufw     bool         `json:"ufw"`
}

type SysctlView struct {
	Key  string `json:"key"`
	Want string `json:"want"`
	Have string `json:"have"`
}

type PeerView struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Address6 string `json:"address6,omitempty"`
	Paired   bool   `json:"paired"`
	InSync   bool   `json:"in_sync"`
	Applied  uint64 `json:"applied"`
	Version  string `json:"version,omitempty"`
	Arch     string `json:"arch,omitempty"`
	LastSeen int64  `json:"last_seen,omitempty"` // unix s
	Tunnels  int    `json:"tunnels"`
}

type ForwardView struct {
	Tunnel string `json:"tunnel"`
	Peer   string `json:"peer,omitempty"`
	Proto  string `json:"proto"`
	Ports  string `json:"ports"`
	To     uint16 `json:"to,omitempty"`
	Target string `json:"target"` // far inner address
	Public string `json:"public"` // this node's public address
	Mode   string `json:"mode"`
	ProxyV int    `json:"proxy_version,omitempty"`
	// Traffic of this port, the client's own bytes before the tunnel wraps
	// them. Rx/Tx are absolute totals; the panel diffs them into a rate.
	Rx    uint64       `json:"rx"`
	Tx    uint64       `json:"tx"`
	Today traffic.Pair `json:"today"`
}

type PanelInfo struct {
	URL  string `json:"url"`
	Port int    `json:"port"`
	User string `json:"user"`
}

// CommandGroup is a set of ready-to-run shell commands shown in the panel,
// filled with this node's real names and addresses.
type CommandGroup struct {
	Title    string    `json:"title"`
	Commands []Command `json:"commands"`
}

type Command struct {
	Label string `json:"label"`
	Run   string `json:"run"`
	Where string `json:"where"` // which server to run it on
}

// HealthView is a tunnel's live ping and loss, from traffic the daemon
// sends through it (so "passes" means packets really get across).
type HealthView struct {
	Ping   float64 `json:"ping_ms"`
	Loss   float64 `json:"loss"`
	Passes bool    `json:"passes"`
	Last   int64   `json:"last"`
}
