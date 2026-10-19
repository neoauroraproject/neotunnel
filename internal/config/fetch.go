package config

import (
	"fmt"
	"slices"
	"strings"
)

// Fetch sends this server's own downloads from listed sites (GitHub, the
// Ubuntu archive, ...) through a tunnel, so they leave to the internet from
// the far server: for when this server's own way out is cut or the site is
// filtered here. Everything else keeps going out directly.
//
// The daemon redirects the server's outgoing HTTP and HTTPS connections to
// a local listener (FetchPort), reads the site's name from the first bytes
// (the TLS server name or the HTTP Host header) and sends the listed ones
// through the tunnel like a socks forward's connections; the rest go on to
// where they were going.
type Fetch struct {
	On     bool   `json:"on,omitempty"`
	Tunnel string `json:"tunnel,omitempty"`
	// Groups are FetchGroups names; Domains are more sites, each with its
	// subdomains.
	Groups  []string `json:"groups,omitempty"`
	Domains []string `json:"domains,omitempty"`
	// Strict never lets a listed site go out directly, even while the
	// tunnel is down.
	Strict bool `json:"strict,omitempty"`
	// All takes every program's connections; otherwise only those of the
	// commands people run in a terminal or over SSH (user.slice).
	All bool `json:"all,omitempty"`
}

// FetchPort is the local port the daemon takes redirected connections on;
// the far node knows a tunnel's downloads by a socks forward on it.
const FetchPort = 7713

// FetchGroup is a preset list of sites.
type FetchGroup struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Domains []string `json:"domains"`
}

// FetchGroups are the presets the panel offers.
var FetchGroups = []FetchGroup{
	{"github", "GitHub", []string{"github.com", "githubusercontent.com", "githubassets.com", "github.io", "ghcr.io"}},
	{"ubuntu", "مخازن اوبونتو (apt)", []string{"archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com", "ppa.launchpadcontent.net", "keyserver.ubuntu.com"}},
	{"pypi", "PyPI (pip)", []string{"pypi.org", "pythonhosted.org"}},
	{"node", "Node.js و npm", []string{"npmjs.org", "nodejs.org", "yarnpkg.com"}},
	{"go", "Go", []string{"golang.org", "go.dev"}},
	{"letsencrypt", "Let's Encrypt", []string{"letsencrypt.org"}},
}

// Sites lists every site the downloads take through the tunnel.
func (f Fetch) Sites() []string {
	var out []string
	for _, g := range FetchGroups {
		if slices.Contains(f.Groups, g.Name) {
			out = append(out, g.Domains...)
		}
	}
	for _, d := range f.Domains {
		if d = CleanDomain(d); d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// CleanDomain lowercases a site name and drops a scheme, path, port,
// leading "*." or trailing dot ("" when nothing usable is left).
func CleanDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if _, rest, ok := strings.Cut(d, "://"); ok {
		d = rest
	}
	d, _, _ = strings.Cut(d, "/")
	if h, _, ok := strings.Cut(d, ":"); ok && !strings.Contains(d, "]") {
		d = h
	}
	d = strings.TrimPrefix(strings.TrimSuffix(d, "."), "*.")
	return d
}

// ValidDomain reports whether d is a host name the downloads can match.
func ValidDomain(d string) error {
	if len(d) == 0 || len(d) > 253 || !strings.Contains(d, ".") {
		return fmt.Errorf("bad site name %q", d)
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("bad site name %q", d)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("bad site name %q", d)
			}
		}
	}
	return nil
}

// FetchActive is the downloads' configuration when they are on.
func (s *State) FetchActive() (Fetch, bool) {
	if s.Fetch == nil || !s.Fetch.On || s.Fetch.Tunnel == "" {
		return Fetch{}, false
	}
	return *s.Fetch, true
}

// fetchForward is how a peer learns that this node's downloads leave
// through it: a proxy forward on the tunnel, which every NeoTunnel version
// with proxies already serves (see forward.relayExits).
func (s *State) fetchForward(peer string) (Forward, bool) {
	f, ok := s.FetchActive()
	if !ok {
		return Forward{}, false
	}
	t, ok := s.Tunnel(f.Tunnel)
	if !ok {
		return Forward{}, false
	}
	if _, ok := t.SideOf(peer); !ok {
		return Forward{}, false
	}
	return Forward{Node: s.Self, Tunnel: t.Name, Proto: "tcp", Ports: fmt.Sprint(FetchPort), Mode: ForwardSocks,
		Listen: "127.0.0.1", Fetch: true}, true
}
