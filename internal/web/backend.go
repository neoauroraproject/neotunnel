package web

import (
	"context"
	"time"

	"neotunnel/internal/bench"
	"neotunnel/internal/config"
	"neotunnel/internal/failover"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
	"neotunnel/internal/metrics"
	"neotunnel/internal/safe"
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
	// RestartTunnel takes a tunnel down and up on both servers, settings unchanged.
	RestartTunnel(ctx context.Context, name string) error
	// MikroTik returns the RouterOS commands for a router at the far end.
	MikroTik(name string) (string, error)
	// RandomPort picks a free random port for a tunnel (proto tcp or udp).
	RandomPort(proto string) (uint16, error)
	RemoveTunnel(name string) (manage.Change, error)
	AddForward(req manage.ForwardRequest) (manage.Change, error)
	UpdateForward(req manage.ForwardRequest) (manage.Change, error)
	RemoveForward(tunnel, ports string) (manage.Change, error)
	SetNode(name, address string) error
	SetDefaults(d config.Defaults) error
	TestTunnel(ctx context.Context, name string, o SpeedOptions, progress func(string), live func(string, float64)) ([]bench.Result, error)
	Bench(ctx context.Context, peer string, only []string, o SpeedOptions, progress func(string), live func(string, float64), partial func(bench.Result)) ([]bench.Result, error)
	// Check brings a would-be tunnel up for a few seconds and reports whether
	// traffic passes (all: every kind at once).
	// With speed it also measures bandwidth, a few seconds each way.
	Check(ctx context.Context, req manage.TunnelRequest, all, speed bool) ([]bench.Result, error)
	// ProbeMTU measures the path MTU under a tunnel and remembers it.
	ProbeMTU(ctx context.Context, name string) (MTUReport, error)
	Traffic() map[string]traffic.Summary
	// Metrics is the graphs history: keys (all when empty) over a range
	// (1h, 6h, 24h, 7d, 30d, 90d or all).
	Metrics(rng string, keys []string) (metrics.View, error)
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

	// Safely runs change as a safe one (see package safe): reverted on both
	// servers unless kept with KeepSafe in time.
	Safely(what string, change func() (manage.Change, error)) (manage.Change, *SafeView, error)
	// SafeStatus is the safe change waiting for confirmation, if any, and
	// how the last one ended.
	SafeStatus() (*SafeView, *safe.Outcome)
	// KeepSafe confirms a safe change from the panel; the outcome is set
	// once the peers confirmed too.
	KeepSafe(id string) (*safe.Outcome, error)
	UndoSafe(id string) (*safe.Outcome, error)
}

// SpeedOptions are a speed test's length per direction and number of
// connections, as chosen in the panel; zero means the default.
type SpeedOptions struct {
	Seconds int `json:"seconds"`
	Streams int `json:"streams"`
}

// apply sets the chosen values on o, within what the probe server allows.
func (s SpeedOptions) apply(o *bench.Options) {
	if s.Seconds > 0 {
		o.Duration = time.Duration(min(max(s.Seconds, 3), 30)) * time.Second
	}
	if s.Streams > 0 {
		o.Streams = min(s.Streams, 32)
	}
}

// SafeView is a safe change waiting for confirmation, as the panel sees it.
type SafeView struct {
	ID       string   `json:"id"`
	What     string   `json:"what"`
	Deadline int64    `json:"deadline"` // unix ms
	Peers    []string `json:"peers,omitempty"`
	Kept     bool     `json:"kept,omitempty"`
	Acked    []string `json:"acked,omitempty"`
}

type Overview struct {
	Node NodeInfo `json:"node"`
	// Master is the first server that manages this one (older panels read
	// it); Masters are all of them.
	Master   *MasterInfo     `json:"master,omitempty"`
	Masters  []MasterInfo    `json:"masters,omitempty"`
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
	Safe     *SafeView       `json:"safe,omitempty"`      // a safe change waiting for confirmation
	SafeLast *safe.Outcome   `json:"safe_last,omitempty"` // how the last one ended
	Install  string          `json:"install"`             // one-line installer for a new server
	Repo     string          `json:"repo"`
	Time     int64           `json:"time"` // unix ms, for rate calculation
}

type NodeInfo struct {
	Name     string     `json:"name"`
	Address  string     `json:"address"`
	Address6 string     `json:"address6,omitempty"`
	AESNI    bool       `json:"aes_ni"`
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
	AESNI    *bool  `json:"aes_ni,omitempty"`
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
	// Listen and Auth of a socks forward (a proxy on this server).
	Listen string `json:"listen,omitempty"`
	Auth   string `json:"auth,omitempty"`
	// Traffic of this port, the client's own bytes before the tunnel wraps
	// them. Rx/Tx are absolute totals; the panel diffs them into a rate.
	Rx    uint64       `json:"rx"`
	Tx    uint64       `json:"tx"`
	Today traffic.Pair `json:"today"`
	// Backup is the tunnel the forward moves to when Tunnel fails, Active
	// the one it runs on now, Since when it last moved and Switches its
	// recent moves, oldest first.
	Backup   string            `json:"backup,omitempty"`
	Active   string            `json:"active"`
	Since    int64             `json:"since,omitempty"`
	Switches []failover.Switch `json:"switches,omitempty"`
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
	// Probes is how many probes the figures come from; the panel trusts
	// them from 3 on.
	Probes int `json:"probes"`
}
