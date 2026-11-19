package manage

// NeoExit on this server and on the peers, from one panel: which server
// has it, installing and removing it, and passing the NeoExit page's
// requests to the right one.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"time"

	"neotunnel/internal/agent"
	"neotunnel/internal/config"
	"neotunnel/internal/fleet"
	"neotunnel/internal/identity"
	"neotunnel/internal/neoexit"
)

// ExitSelf names this server where a NeoExit server is asked for (peers
// go by their names, which never start with "_").
const ExitSelf = "_"

// ExitServer is NeoExit on one server.
type ExitServer struct {
	Key     string `json:"key"` // ExitSelf or the peer's name
	Name    string `json:"name"`
	Address string `json:"address"`
	Arch    string `json:"arch,omitempty"`
	// State is none, installed or running; "" when it isn't known: the
	// server isn't reachable (Reachable) or its NeoTunnel is too old to
	// manage NeoExit (Old).
	State     string          `json:"state"`
	Reachable bool            `json:"reachable"`
	Old       bool            `json:"old,omitempty"`
	Status    *neoexit.Status `json:"status,omitempty"`
}

// ExitServers lists this server and each paired peer with their NeoExit.
func ExitServers(st *config.State) []ExitServer {
	self := ExitServer{Key: ExitSelf, Name: st.Self, Address: st.Address, Arch: runtime.GOARCH,
		State: neoexit.LocalState(), Reachable: true, Status: neoexit.Local()}
	if st.Label != "" {
		self.Name = st.Label
	}
	out := []ExitServer{self}
	for _, p := range st.Peers {
		if !p.Paired() {
			continue
		}
		ps := neoexit.Peer(p.Name)
		x := ExitServer{Key: p.Name, Name: p.Name, Address: p.Address, Arch: p.Arch, State: ps.State, Status: ps.Status,
			Reachable: !p.LastSeen.IsZero() && time.Since(p.LastSeen) < 3*time.Minute}
		if p.Label != "" {
			x.Name = p.Label
		}
		x.Old = x.Reachable && ps.State == ""
		out = append(out, x)
	}
	return out
}

func exitPeer(name string, timeout time.Duration) (*agent.Client, config.Peer, error) {
	st, err := Node()
	if err != nil {
		return nil, config.Peer{}, err
	}
	p, ok := st.Peer(name)
	if !ok || !p.Paired() {
		return nil, config.Peer{}, fmt.Errorf("unknown server %q", name)
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return nil, *p, err
	}
	c, err := agent.ToPeerFor(id, *p, timeout)
	return c, *p, err
}

// ExitCall passes a request to NeoExit's local API on a server.
func ExitCall(ctx context.Context, server, method, path string, body []byte) (int, []byte, error) {
	if server == ExitSelf {
		return neoexit.Call(ctx, method, path, body)
	}
	c, _, err := exitPeer(server, 2*time.Minute)
	if err != nil {
		return 0, nil, err
	}
	r, err := c.NeoExitCall(ctx, agent.NeoExitCall{Method: method, Path: path, Body: body})
	if errors.Is(err, agent.ErrUnsupported) {
		return 0, nil, fmt.Errorf("NeoTunnel on %s is too old to manage NeoExit; upgrade it first", server)
	}
	return r.Status, r.Body, err
}

// ExitInstall installs (or upgrades to) NeoExit version ("latest" or
// vX.Y.Z) on a server. The server downloads it from GitHub; one that can't
// gets the files from this server, which downloads them itself or has
// another of its servers do it.
func ExitInstall(ctx context.Context, server, version string) (neoexit.Installed, error) {
	if version == "" {
		version = "latest"
	}
	if !neoexit.ValidVersion(version) {
		return neoexit.Installed{}, fmt.Errorf("bad version %q", version)
	}
	if server == ExitSelf {
		v, err := obtain(ctx, version, runtime.GOARCH, "")
		if err != nil {
			return neoexit.Installed{}, err
		}
		return neoexit.Install(ctx, v)
	}
	c, p, err := exitPeer(server, 7*time.Minute)
	if err != nil {
		return neoexit.Installed{}, err
	}
	res, err := c.NeoExitInstall(ctx, version)
	if errors.Is(err, agent.ErrNeedFiles) {
		arch := p.Arch
		if arch == "" {
			arch = "amd64"
		}
		v, ferr := obtain(ctx, version, arch, server)
		if ferr != nil {
			return res, fmt.Errorf("%s couldn't download NeoExit, and neither could this server or the others: %v", server, ferr)
		}
		if err := sendRelease(ctx, c, v, arch); err != nil {
			return res, fmt.Errorf("send NeoExit to %s: %w", server, err)
		}
		neoexit.PruneCache(v) // keep one release here, for the next server that needs it
		res, err = c.NeoExitInstall(ctx, v)
	}
	if errors.Is(err, agent.ErrUnsupported) {
		err = fmt.Errorf("NeoTunnel on %s is too old to install NeoExit; upgrade it first", server)
	}
	if err == nil {
		refreshPeer(server)
	}
	return res, err
}

// ExitUninstall removes NeoExit and its settings from a server.
func ExitUninstall(ctx context.Context, server string) error {
	if server == ExitSelf {
		return neoexit.Uninstall()
	}
	c, _, err := exitPeer(server, time.Minute)
	if err != nil {
		return err
	}
	if err := c.NeoExitUninstall(ctx); err != nil {
		return err
	}
	refreshPeer(server)
	return nil
}

// obtain puts release version for arch in this server's cache: downloaded
// here, else by another server (not skip). With "latest" and nobody able
// to reach GitHub, the newest release already in the cache will do.
func obtain(ctx context.Context, version, arch, skip string) (string, error) {
	v, err := neoexit.Fetch(ctx, version, arch)
	if err == nil {
		return v, nil
	}
	if v, err = fetchVia(ctx, version, arch, skip); err == nil {
		return v, nil
	}
	if version == "latest" {
		if c := neoexit.NewestCached(arch); c != "" {
			return c, nil
		}
	}
	return "", err
}

// fetchVia has a peer (not skip) download NeoExit for arch and copies the
// files into this server's cache. It reports the version.
func fetchVia(ctx context.Context, version, arch, skip string) (string, error) {
	st, err := Node()
	if err != nil {
		return "", err
	}
	errs := []error{errors.New("this server: no answer from GitHub")}
	for _, p := range st.Peers {
		if !p.Paired() || p.Name == skip || neoexit.Peer(p.Name).State == "" {
			continue
		}
		c, _, err := exitPeer(p.Name, 6*time.Minute)
		if err != nil {
			continue
		}
		v, err := c.NeoExitFetch(ctx, version, arch)
		if err == nil {
			err = copyRelease(ctx, c, v, arch)
		}
		if err == nil {
			return v, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
	}
	return "", errors.Join(errs...)
}

// copyRelease copies a release a peer has in its cache into ours.
func copyRelease(ctx context.Context, c *agent.Client, version, arch string) error {
	prog, psiphon := neoexit.FileNames(arch)
	for _, name := range []string{psiphon, prog} {
		f, err := c.NeoExitGet(ctx, version, name)
		if err != nil {
			if name == prog {
				return err
			}
			continue
		}
		if err := neoexit.SaveCached(version, name, bytesReader(f.Data), f.Sum); err != nil {
			return err
		}
	}
	return nil
}

// sendRelease sends a release from our cache to a peer's.
func sendRelease(ctx context.Context, c *agent.Client, version, arch string) error {
	prog, psiphon := neoexit.FileNames(arch)
	for _, name := range []string{psiphon, prog} {
		p, err := neoexit.CachePath(version, name)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			if name == prog {
				return err
			}
			continue
		}
		sum, err := neoexit.FileSum(p)
		if err != nil {
			return err
		}
		if err := c.NeoExitPut(ctx, version, name, b, sum); err != nil {
			return err
		}
	}
	return nil
}

// refreshPeer has a peer's NeoExit status read again now.
func refreshPeer(name string) {
	st, err := Node()
	if err != nil || !slices.ContainsFunc(st.Peers, func(p config.Peer) bool { return p.Name == name }) {
		return
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fleet.Refresh(ctx, id, st, name)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// ExitServersNow is ExitServers after asking each peer now: for the CLI,
// which doesn't keep what peers said the way the service does.
func ExitServersNow(ctx context.Context) ([]ExitServer, error) {
	id, err := identity.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	fleet.RefreshAll(ctx, id)
	st, err := Node()
	if err != nil {
		return nil, err
	}
	return ExitServers(st), nil
}
