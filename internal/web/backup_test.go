package web

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"neotunnel/internal/config"
)

// Restoring on a server with another IP moves this server's side of its
// tunnels, keeps this panel's login and brings the key back.
func TestRestoreOnNewServer(t *testing.T) {
	old := config.Dir
	config.Dir = t.TempDir()
	defer func() { config.Dir = old }()
	backup := config.State{Self: "ir", Address: "192.0.2.1", Peers: []config.Peer{{Name: "de", Address: "198.51.100.2"}},
		Tunnels: []config.Tunnel{{Name: "t1", Sides: [2]config.Side{{Node: "ir", Endpoint: "192.0.2.1"}, {Node: "de", Endpoint: "198.51.100.2"}}}},
		Panel:   &config.Panel{Port: 1111, Path: "/old", User: "old", PassHash: "x", Domain: "p.example.org", Cert: config.CertCustom}}
	raw, _ := json.Marshal(backup)
	b, _ := json.Marshal(Bundle{Format: backupFormat, Version: "v0.8.0", State: raw, Files: map[string]string{
		"identity.key":     base64.StdEncoding.EncodeToString([]byte("KEY")),
		"panel-domain.crt": base64.StdEncoding.EncodeToString([]byte("CRT")),
	}})
	if err := config.Save(&config.State{Self: "fresh", Address: "203.0.113.9", Panel: &config.Panel{Port: 2222, Path: "/new", User: "admin", PassHash: "y"}}); err != nil {
		t.Fatal(err)
	}
	l := &Live{}
	p, err := l.InspectBackup(b)
	if err != nil || p.Node != "ir" || p.Here != "203.0.113.9" || !p.Key || p.SameKey || p.Tunnels != 1 {
		t.Fatalf("preview %+v %v", p, err)
	}
	if _, err := l.Restore(b); err != nil {
		t.Fatal(err)
	}
	st, _ := config.Load()
	if st.Self != "ir" || st.Address != "203.0.113.9" || st.Tunnels[0].Sides[0].Endpoint != "203.0.113.9" || st.Tunnels[0].Sides[1].Endpoint != "198.51.100.2" {
		t.Errorf("addresses: %+v %+v", st.Address, st.Tunnels[0].Sides)
	}
	if pn := st.Panel; pn.Port != 2222 || pn.Path != "/new" || pn.User != "admin" || pn.Domain != "p.example.org" {
		t.Errorf("panel %+v", pn)
	}
	if k, _ := os.ReadFile(filepath.Join(config.Dir, "identity.key")); string(k) != "KEY" {
		t.Errorf("key %q", k)
	}
	for _, bad := range []string{`{"format":"other"}`, `not json`, `{"format":"neotunnel-backup","state":{},"files":{"../x":""}}`} {
		if _, err := l.InspectBackup([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
