package web

import (
	"context"

	"neotunnel/internal/bench"
	"neotunnel/internal/fleet"
	"neotunnel/internal/manage"
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
	RemoveTunnel(name string) (manage.Change, error)
	AddForward(tunnel, ports, proto string, to uint16) (manage.Change, error)
	RemoveForward(tunnel, ports string) (manage.Change, error)
	SetNode(name, address string) error
	TestTunnel(ctx context.Context, name string, progress func(string)) ([]bench.Result, error)
	Bench(ctx context.Context, peer string, only []string, progress func(string)) ([]bench.Result, error)

	Login(user, pass string) bool
	ChangePassword(current, next string) error
	// SetPanelPort saves a new panel port and returns the new URL; the panel
	// restarts on it right after responding.
	SetPanelPort(port int) (string, error)
}

type Overview struct {
	Node     NodeInfo       `json:"node"`
	Master   *MasterInfo    `json:"master,omitempty"`
	Revision uint64         `json:"revision"`
	Tunnels  []TunnelView   `json:"tunnels"`
	Peers    []PeerView     `json:"peers"`
	Forwards []ForwardView  `json:"forwards"`
	Modes    []manage.Mode  `json:"modes"`
	Errors   []string       `json:"errors,omitempty"`
	Panel    PanelInfo      `json:"panel"`
	Commands []CommandGroup `json:"commands"`
	Install  string         `json:"install"` // one-line installer for a new server
	Repo     string         `json:"repo"`
	Time     int64          `json:"time"` // unix ms, for rate calculation
}

type NodeInfo struct {
	Name     string     `json:"name"`
	Address  string     `json:"address"`
	Version  string     `json:"version"`
	Arch     string     `json:"arch"`
	Key      string     `json:"key"`
	Uptime   int64      `json:"uptime_s"`
	Load     [3]float64 `json:"load"`
	CPUs     int        `json:"cpus"`
	MemTotal uint64     `json:"mem_total"`
	MemUsed  uint64     `json:"mem_used"`
	BBR      bool       `json:"bbr"`
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
}

type PeerView struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
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
