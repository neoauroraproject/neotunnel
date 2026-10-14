package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vishvananda/netlink"

	"neotunnel/internal/config"
)

// FOU receive ports are kernel-wide and carry no owner mark, so the ones we
// add are recorded in fou.json: "port/proto" -> expiry (0 = persistent).

type fouKey struct{ Port, Proto int }

func (k fouKey) String() string { return fmt.Sprintf("%d/%d", k.Port, k.Proto) }

func fouPath() string { return filepath.Join(config.Dir, "fou.json") }

func loadOwned() map[string]int64 {
	owned := map[string]int64{}
	if b, err := os.ReadFile(fouPath()); err == nil {
		json.Unmarshal(b, &owned)
	}
	return owned
}

func saveOwned(owned map[string]int64) {
	b, _ := json.Marshal(owned)
	os.WriteFile(fouPath(), b, 0o600)
}

func fouKeys(specs []LinkSpec) map[string]fouKey {
	out := map[string]fouKey{}
	for _, s := range specs {
		if !s.FOU {
			continue
		}
		ki, err := config.LookupKind(s.Kind)
		if err != nil {
			continue
		}
		k := fouKey{int(s.Port), ki.IPProto}
		out[k.String()] = k
	}
	return out
}

func fouExisting() map[string]bool {
	have := map[string]bool{}
	fs, err := netlink.FouList(netlink.FAMILY_V4)
	if err != nil {
		return have
	}
	for _, f := range fs {
		have[fouKey{f.Port, f.Protocol}.String()] = true
	}
	return have
}

func fouAdd(k fouKey) error {
	err := netlink.FouAdd(netlink.Fou{Family: netlink.FAMILY_V4, Port: k.Port, Protocol: k.Proto, EncapType: netlink.FOU_ENCAP_DIRECT})
	if err != nil {
		return fmt.Errorf("open UDP port %d for the tunnel: %w (port in use, or 'fou' kernel module missing)", k.Port, err)
	}
	return nil
}

func fouDel(key string) {
	var k fouKey
	if _, err := fmt.Sscanf(key, "%d/%d", &k.Port, &k.Proto); err == nil {
		netlink.FouDel(netlink.Fou{Family: netlink.FAMILY_V4, Port: k.Port, Protocol: k.Proto})
	}
}

// syncFOU opens the receive ports persistent tunnels need and closes ones we
// opened that are no longer used (or expired bench ports).
func syncFOU(specs []LinkSpec) []error {
	var errs []error
	config.WithLock(func() error {
		want := fouKeys(specs)
		owned, have := loadOwned(), fouExisting()
		now := time.Now().Unix()
		for key, k := range want {
			if !have[key] {
				if err := fouAdd(k); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			if _, mine := owned[key]; mine || !have[key] {
				owned[key] = 0
			}
		}
		for key, exp := range owned {
			if _, wanted := want[key]; wanted {
				continue
			}
			if exp == 0 || now > exp {
				fouDel(key)
				delete(owned, key)
			}
		}
		saveOwned(owned)
		return nil
	})
	return errs
}

// addBenchFOU opens receive ports for temporary tunnels until exp.
func addBenchFOU(specs []LinkSpec, exp time.Time) error {
	return config.WithLock(func() error {
		owned, have := loadOwned(), fouExisting()
		defer saveOwned(owned)
		for key, k := range fouKeys(specs) {
			if have[key] {
				continue
			}
			if err := fouAdd(k); err != nil {
				return err
			}
			owned[key] = exp.Unix()
		}
		return nil
	})
}

// removeAllFOU closes every port we opened.
func removeAllFOU() {
	config.WithLock(func() error {
		for key := range loadOwned() {
			fouDel(key)
		}
		saveOwned(map[string]int64{})
		return nil
	})
}
