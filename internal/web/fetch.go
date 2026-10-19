package web

import (
	"context"
	"crypto/tls"
	"os"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
	"neotunnel/internal/relay"
)

// FetchView is the downloads-through-a-tunnel settings page.
type FetchView struct {
	Fetch   config.Fetch        `json:"fetch"`
	Groups  []config.FetchGroup `json:"groups"`
	Tunnels []string            `json:"tunnels"` // the ones it can go through
	Status  relay.FetchStats    `json:"status"`
	// Terminal tells whether this server can take only terminal and SSH
	// commands (a unified cgroup hierarchy with user.slice).
	Terminal bool `json:"terminal"`
}

// FetchProbe is one way of reaching a site in the downloads test.
type FetchProbe struct {
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms,omitempty"`
	Error string `json:"error,omitempty"`
}

// FetchTest compares reaching a site through the tunnel and directly: a TLS
// handshake each way.
type FetchTest struct {
	Host   string     `json:"host"`
	Tunnel FetchProbe `json:"tunnel"`
	Direct FetchProbe `json:"direct"`
}

func (l *Live) FetchSettings() (FetchView, error) {
	st, err := manage.Node()
	if err != nil {
		return FetchView{}, err
	}
	v := FetchView{Groups: config.FetchGroups, Tunnels: manage.FetchTunnels(st), Status: relay.FetchStatus(), Terminal: terminalScope()}
	if st.Fetch != nil {
		v.Fetch = *st.Fetch
	} else {
		v.Fetch = config.Fetch{Groups: []string{"github", "ubuntu"}}
		if len(v.Tunnels) > 0 {
			v.Fetch.Tunnel = v.Tunnels[0]
		}
	}
	if v.Fetch.Groups == nil {
		v.Fetch.Groups = []string{}
	}
	if v.Fetch.Domains == nil {
		v.Fetch.Domains = []string{}
	}
	return v, nil
}

func (l *Live) SetFetch(f config.Fetch) (manage.Change, error) {
	ch, err := manage.SetFetch(f)
	if err == nil && l.Reconcile != nil {
		l.Reconcile()
	}
	return ch, err
}

// TestFetch reaches host on port 443 through the tunnel and directly.
func (l *Live) TestFetch(ctx context.Context, host string) FetchTest {
	host = config.CleanDomain(host)
	if host == "" {
		host = "github.com"
	}
	r := FetchTest{Host: host}
	probe := func(way string) FetchProbe {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		t0 := time.Now()
		c, err := relay.FetchDial(ctx, host+":443", way)
		if err != nil {
			return FetchProbe{Error: err.Error()}
		}
		defer c.Close()
		tc := tls.Client(c, &tls.Config{ServerName: host})
		if err := tc.HandshakeContext(ctx); err != nil {
			return FetchProbe{Error: err.Error()}
		}
		return FetchProbe{OK: true, Ms: time.Since(t0).Milliseconds()}
	}
	done := make(chan struct{})
	go func() {
		r.Direct = probe("direct")
		close(done)
	}()
	r.Tunnel = probe("tunnel")
	<-done
	return r
}

// terminalScope reports whether the redirect can tell terminal and SSH
// sessions (user.slice) from services.
func terminalScope() bool {
	_, err1 := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	_, err2 := os.Stat("/sys/fs/cgroup/user.slice")
	return err1 == nil && err2 == nil
}
