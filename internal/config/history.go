package config

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The configuration's history: before each change to the network (tunnels,
// ports, pools, defaults, downloads), what it was is kept, so the change
// can be undone in one step. What isn't the network stays out of it: who
// this server is, its peers, the panel's password and tokens, the firewall
// and the Telegram bot are never put back to an older state.

// Network is the part of the state a rollback puts back.
type Network struct {
	Tunnels   []Tunnel      `json:"tunnels,omitempty"`
	Forwards  []Forward     `json:"forwards,omitempty"`
	Pools     []AddressPool `json:"pools,omitempty"`
	PortOrder []string      `json:"port_order,omitempty"`
	Defaults  *Defaults     `json:"defaults,omitempty"`
	Fetch     *Fetch        `json:"fetch,omitempty"`
}

// NetworkOf is the network part of a state.
func NetworkOf(s *State) Network {
	return Network{Tunnels: s.Tunnels, Forwards: s.Forwards, Pools: s.Pools, PortOrder: s.PortOrder, Defaults: s.Defaults, Fetch: s.Fetch}
}

// Put sets the network part of a state.
func (n Network) Put(s *State) {
	s.Tunnels, s.Forwards, s.Pools, s.PortOrder, s.Defaults, s.Fetch = n.Tunnels, n.Forwards, n.Pools, n.PortOrder, n.Defaults, n.Fetch
}

// Diff is one thing a change did: Kind is tunnel, port, pool, defaults or
// downloads; Op added, removed or changed; Name the tunnel's name or the
// port's protocol/ports.
type Diff struct {
	Kind string `json:"kind"`
	Op   string `json:"op"`
	Name string `json:"name,omitempty"`
}

// Diffs lists what changed from a to b.
func Diffs(a, b Network) []Diff {
	var out []Diff
	same := func(x, y any) bool {
		bx, _ := json.Marshal(x)
		by, _ := json.Marshal(y)
		return bytes.Equal(bx, by)
	}
	list := func(kind string, a, b map[string]any, order []string) {
		for _, k := range order {
			x, inA := a[k]
			y, inB := b[k]
			switch {
			case inA && !inB:
				out = append(out, Diff{kind, "removed", k})
			case !inA && inB:
				out = append(out, Diff{kind, "added", k})
			case !same(x, y):
				out = append(out, Diff{kind, "changed", k})
			}
		}
	}
	keys := func(ma, mb map[string]any) []string {
		var ks []string
		for k := range ma {
			ks = append(ks, k)
		}
		for k := range mb {
			if _, ok := ma[k]; !ok {
				ks = append(ks, k)
			}
		}
		sort.Strings(ks)
		return ks
	}
	ta, tb := map[string]any{}, map[string]any{}
	for _, t := range a.Tunnels {
		ta[t.Name] = t
	}
	for _, t := range b.Tunnels {
		tb[t.Name] = t
	}
	list("tunnel", ta, tb, keys(ta, tb))
	fa, fb := map[string]any{}, map[string]any{}
	for _, f := range a.Forwards {
		fa[f.Node+" "+f.PortKey()] = f
	}
	for _, f := range b.Forwards {
		fb[f.Node+" "+f.PortKey()] = f
	}
	n := len(out)
	list("port", fa, fb, keys(fa, fb))
	for i := n; i < len(out); i++ {
		_, out[i].Name, _ = strings.Cut(out[i].Name, " ") // the node's name off
	}
	pa, pb := map[string]any{}, map[string]any{}
	for _, p := range a.Pools {
		pa[p.Name] = p
	}
	for _, p := range b.Pools {
		pb[p.Name] = p
	}
	list("pool", pa, pb, keys(pa, pb))
	if !same(a.Defaults, b.Defaults) {
		out = append(out, Diff{Kind: "defaults", Op: "changed"})
	}
	if !same(a.Fetch, b.Fetch) {
		out = append(out, Diff{Kind: "downloads", Op: "changed"})
	}
	if len(out) == 0 && !slices.Equal(a.PortOrder, b.PortOrder) {
		out = append(out, Diff{Kind: "order", Op: "changed"})
	}
	return out
}

// Snapshot is one entry of the history: the network as it was before a
// change, and what that change did.
type Snapshot struct {
	ID       string `json:"id"`
	At       int64  `json:"at"`       // unix seconds
	Revision uint64 `json:"revision"` // the revision the change made
	Changes  []Diff `json:"changes"`
	// Rollback marks a change that was itself a rollback, to ID.
	Rollback string `json:"rollback,omitempty"`
}

const (
	historyKeep = 100
	historyAge  = 60 * 24 * time.Hour
)

func historyDir() string { return filepath.Join(Dir, "history") }

// Record keeps before as the network before a change that made revision
// and did changes (nothing when it changed nothing), and forgets entries
// past the last 100 or two months.
func Record(before Network, revision uint64, changes []Diff, rollback string) (Snapshot, error) {
	if len(changes) == 0 {
		return Snapshot{}, nil
	}
	if err := os.MkdirAll(historyDir(), 0o700); err != nil {
		return Snapshot{}, err
	}
	now := time.Now()
	s := Snapshot{ID: strconv.FormatInt(now.UnixNano(), 36), At: now.Unix(), Revision: revision, Changes: changes, Rollback: rollback}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(struct {
		Snapshot
		Network Network `json:"network"`
	}{s, before}); err != nil {
		return Snapshot{}, err
	}
	zw.Close()
	if err := os.WriteFile(filepath.Join(historyDir(), s.ID+".json.gz"), buf.Bytes(), 0o600); err != nil {
		return Snapshot{}, err
	}
	trimHistory(now)
	return s, nil
}

// History lists the entries, newest first.
func History() []Snapshot {
	var out []Snapshot
	for _, id := range historyIDs() {
		if s, _, err := ReadSnapshot(id); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// ReadSnapshot reads one entry with the network it kept.
func ReadSnapshot(id string) (Snapshot, Network, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return Snapshot{}, Network{}, fmt.Errorf("no change %q in the history", id)
	}
	b, err := os.ReadFile(filepath.Join(historyDir(), id+".json.gz"))
	if err != nil {
		return Snapshot{}, Network{}, fmt.Errorf("no change %q in the history", id)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return Snapshot{}, Network{}, err
	}
	var v struct {
		Snapshot
		Network Network `json:"network"`
	}
	if err := json.NewDecoder(zr).Decode(&v); err != nil {
		return Snapshot{}, Network{}, err
	}
	return v.Snapshot, v.Network, nil
}

// historyIDs are the entries' IDs, newest first (they sort by time).
func historyIDs() []string {
	es, _ := os.ReadDir(historyDir())
	var ids []string
	for _, e := range es {
		if id, ok := strings.CutSuffix(e.Name(), ".json.gz"); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.ParseInt(ids[i], 36, 64)
		b, _ := strconv.ParseInt(ids[j], 36, 64)
		return a > b
	})
	return ids
}

func trimHistory(now time.Time) {
	for i, id := range historyIDs() {
		at, _ := strconv.ParseInt(id, 36, 64)
		if i >= historyKeep || now.Sub(time.Unix(0, at)) > historyAge {
			os.Remove(filepath.Join(historyDir(), id+".json.gz"))
		}
	}
}
