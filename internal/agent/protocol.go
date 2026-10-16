// Package agent is the control channel between a master and its peers:
// HTTPS with both sides pinning each other's Ed25519 key.
package agent

import (
	"neotunnel/internal/config"
	"neotunnel/internal/engine"
	"neotunnel/internal/traffic"
)

const DefaultPort = 7710

type RegisterRequest struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	AgentPort int    `json:"agent_port"`
	Revision  uint64 `json:"revision"` // configuration the peer already runs
}

type ApplyRequest struct {
	Revision uint64           `json:"revision"`
	Tunnels  []config.Tunnel  `json:"tunnels"`
	Forwards []config.Forward `json:"forwards"`
}

type ApplyResponse struct {
	Revision uint64   `json:"revision"`
	Errors   []string `json:"errors,omitempty"`
}

type TunnelStatus struct {
	Name    string `json:"name"`
	Iface   string `json:"iface"`
	Kind    string `json:"kind"`
	Up      bool   `json:"up"`
	MTU     int    `json:"mtu"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
	Error   string `json:"error,omitempty"`
}

type Status struct {
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Arch     string         `json:"arch"`
	Revision uint64         `json:"revision"`
	Uptime   int64          `json:"uptime_s"`
	Tunnels  []TunnelStatus `json:"tunnels"`
}

type ProbeRequest struct {
	Token   string `json:"token"`
	Seconds int    `json:"seconds"`
}

type BenchUpRequest struct {
	Self    string          `json:"self"`
	Tunnels []config.Tunnel `json:"tunnels"`
	TTL     int             `json:"ttl_s"`
}

type BenchUpResponse struct {
	Specs []engine.LinkSpec `json:"specs"`
}

type BenchDownRequest struct {
	Names []string `json:"names"`
}

// ExcludeRequest moves a speed test's bytes out of a tunnel's traffic
// totals on the peer.
type ExcludeRequest struct {
	Tunnel string       `json:"tunnel"`
	Bytes  traffic.Pair `json:"bytes"`
}
