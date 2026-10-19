package web

// Backup and restore. A backup is one JSON file with the state, the server's
// key and the files around it (traffic totals, graphs history, the panel's
// certificate), so it can be restored on this server or on a new one:
//
//   - The server's key comes back, so the destination servers, which trust
//     their main server by its key, take orders from the new one at once.
//   - On a server with another public IP, the backup's IP is replaced with
//     the new one in the state and in this server's side of every tunnel,
//     and the change is pushed to the destination servers.
//   - The panel keeps this server's port, address and login, so whoever
//     restores isn't locked out; its domain and certificate come back.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/manage"
	"neotunnel/internal/version"
)

const backupFormat = "neotunnel-backup"

// backupFiles are the files of the state directory a backup carries.
var backupFiles = []string{"identity.key", "traffic.json", "traffic.exclude", "traffic.reset", "failover.json", "pmtu.json",
	"metrics.gob.gz", "panel-domain.crt", "panel-domain.key", "acme-account.key"}

// Bundle is a backup file.
type Bundle struct {
	Format  string            `json:"format"`
	Version string            `json:"version"` // NeoTunnel's, that made it
	Created time.Time         `json:"created"`
	Node    string            `json:"node"`
	Address string            `json:"address,omitempty"`
	State   json.RawMessage   `json:"state"`
	Files   map[string]string `json:"files,omitempty"` // name -> base64
}

// Backup returns this server's backup file.
func (l *Live) Backup() ([]byte, error) {
	var b Bundle
	err := config.WithLock(func() error {
		st, err := config.Load()
		if err != nil {
			return err
		}
		raw, err := json.Marshal(st)
		if err != nil {
			return err
		}
		b = Bundle{Format: backupFormat, Version: version.Version, Created: time.Now().UTC(), Node: st.Self, Address: st.Address,
			State: raw, Files: map[string]string{}}
		for _, n := range backupFiles {
			if data, err := os.ReadFile(filepath.Join(config.Dir, n)); err == nil {
				b.Files[n] = base64.StdEncoding.EncodeToString(data)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(b, "", "  ")
}

// BackupPreview is what a backup file holds and what restoring it here
// would do, shown before the user confirms.
type BackupPreview struct {
	Node     string    `json:"node"`
	Created  time.Time `json:"created,omitempty"`
	Version  string    `json:"version,omitempty"`
	Address  string    `json:"address,omitempty"` // in the backup
	Here     string    `json:"here,omitempty"`    // this server's
	Tunnels  int       `json:"tunnels"`
	Forwards int       `json:"forwards"`
	Peers    []string  `json:"peers"`
	// Key: the backup has the server's key; SameKey: it is this server's.
	Key     bool `json:"key"`
	SameKey bool `json:"same_key"`
	// Managed: the backup is of a destination server, which a main server
	// reaches at its old address.
	Managed  []string `json:"managed,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// parseBackup reads a backup file, or an older backup that was just the
// state.
func parseBackup(data []byte) (Bundle, *config.State, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return b, nil, errors.New("this isn't a NeoTunnel backup file")
	}
	if b.Format == "" {
		// Before v0.8 the backup was the state file itself.
		b = Bundle{State: data}
	} else if b.Format != backupFormat {
		return b, nil, errors.New("this isn't a NeoTunnel backup file")
	}
	var st config.State
	if err := json.Unmarshal(b.State, &st); err != nil || st.Self == "" {
		return b, nil, errors.New("the backup's settings can't be read")
	}
	if st.Schema > config.SchemaVersion {
		return b, nil, fmt.Errorf("the backup was made by a newer NeoTunnel (%s): update this server first", b.Version)
	}
	for n, v := range b.Files {
		if !slicesContains(backupFiles, n) {
			return b, nil, fmt.Errorf("unexpected file %q in the backup", n)
		}
		if _, err := base64.StdEncoding.DecodeString(v); err != nil {
			return b, nil, fmt.Errorf("file %s in the backup is damaged", n)
		}
	}
	return b, &st, nil
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (l *Live) InspectBackup(data []byte) (BackupPreview, error) {
	b, st, err := parseBackup(data)
	if err != nil {
		return BackupPreview{}, err
	}
	cur, err := config.Load()
	if err != nil {
		return BackupPreview{}, err
	}
	p := BackupPreview{Node: st.Self, Created: b.Created, Version: b.Version, Address: st.Address, Here: cur.Address,
		Tunnels: len(st.Tunnels), Forwards: len(st.Forwards), Peers: []string{}}
	for _, peer := range st.Peers {
		p.Peers = append(p.Peers, peer.Name)
	}
	if st.Panel != nil && st.Panel.Cert != "" {
		p.Domain = st.Panel.Domain
	}
	for _, m := range st.Masters() {
		p.Managed = append(p.Managed, m.Master.Name)
	}
	if key, ok := b.Files["identity.key"]; ok {
		p.Key = true
		mine, _ := os.ReadFile(filepath.Join(config.Dir, "identity.key"))
		raw, _ := base64.StdEncoding.DecodeString(key)
		p.SameKey = bytes.Equal(raw, mine)
	} else if len(st.Peers) > 0 {
		p.Warnings = append(p.Warnings, "no-key")
	}
	if st.Address != "" && cur.Address != "" && st.Address != cur.Address && len(p.Managed) > 0 {
		p.Warnings = append(p.Warnings, "managed-moved")
	}
	return p, nil
}

// RestoreResult is how a restore went: the destination servers it pushed
// the restored settings to.
type RestoreResult struct {
	Moved bool              `json:"moved"` // the public IP changed
	Peers []manage.PeerSync `json:"peers,omitempty"`
}

// Restore replaces this server's settings and files with a backup's. The
// daemon must restart afterwards (it holds the old key).
func (l *Live) Restore(data []byte) (RestoreResult, error) {
	b, st, err := parseBackup(data)
	if err != nil {
		return RestoreResult{}, err
	}
	var res RestoreResult
	err = config.WithLock(func() error {
		cur, err := config.Load()
		if err != nil {
			return err
		}
		_, cert := b.Files["panel-domain.crt"]
		restorePanel(st, cur, cert)
		if old, now := st.Address, cur.Address; old != "" && now != "" && old != now {
			moveAddress(st, old, now)
			res.Moved = true
		}
		st.Revision++
		for n, v := range b.Files {
			raw, _ := base64.StdEncoding.DecodeString(v)
			mode := os.FileMode(0o600)
			if n == "panel-domain.crt" {
				mode = 0o644
			}
			if err := writeFile(filepath.Join(config.Dir, n), raw, mode); err != nil {
				return err
			}
		}
		return config.Save(st)
	})
	if err != nil {
		return res, err
	}
	loadPanelCert()
	// The destination servers take the restored settings, signed with the
	// restored key (manage.Sync reads it from disk).
	var peers []string
	for _, p := range st.Peers {
		if p.Paired() {
			peers = append(peers, p.Name)
		}
	}
	res.Peers = manage.Sync(st, peers)
	return res, nil
}

// restorePanel keeps this server's panel access (port, address, login) and
// takes the backup's domain only with its certificate.
func restorePanel(st, cur *config.State, cert bool) {
	domain, mode := "", ""
	if st.Panel != nil {
		domain, mode = st.Panel.Domain, st.Panel.Cert
	}
	if cur.Panel == nil {
		st.Panel = nil
		return
	}
	p := *cur.Panel
	p.Domain, p.Cert = domain, mode
	if !cert {
		p.Domain, p.Cert = "", ""
	}
	st.Panel = &p
}

// moveAddress puts this server's new public IP wherever the backup had the
// old one: its own address and its side of every tunnel it owns.
func moveAddress(st *config.State, old, now string) {
	st.Address = now
	for i := range st.Tunnels {
		for j := range st.Tunnels[i].Sides {
			sd := &st.Tunnels[i].Sides[j]
			if sd.Node == st.Self && sd.Endpoint == old {
				sd.Endpoint = now
			}
		}
	}
}
