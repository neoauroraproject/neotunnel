package guard

// Iran's address ranges (geoip:ir). A copy ships inside the program, so the
// guard works on a server that can't reach GitHub; the daemon replaces it
// with a newer list weekly when it can download one.

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
)

//go:embed geoip-ir.txt
var bundledIR []byte

// The lists the daemon updates from: the same data Xray's geoip:ir uses.
var irSources = []string{
	"https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/text/ir.txt",
	"https://raw.githubusercontent.com/Loyalsoldier/geoip/release/text/ir.txt",
}

const (
	// UpdateEvery is how old the list may get before the daemon fetches a
	// new one.
	UpdateEvery = 7 * 24 * time.Hour
	// minRanges guards against a truncated or wrong download.
	minRanges = 1000
)

func irPath() string { return filepath.Join(config.Dir, "geoip-ir.txt") }

// GeoInfo describes the list in use.
type GeoInfo struct {
	Ranges  int    `json:"ranges"`
	Updated int64  `json:"updated,omitempty"` // unix; 0 = the bundled copy
	Source  string `json:"source"`            // "bundled" or "downloaded"
}

var (
	geoMu    sync.Mutex
	geoCache []netip.Prefix
	geoInfo  GeoInfo
	geoStamp time.Time // mod time of the file the cache came from
)

// IR returns Iran's ranges: the downloaded list when there is a valid one,
// else the bundled copy.
func IR() ([]netip.Prefix, GeoInfo) {
	geoMu.Lock()
	defer geoMu.Unlock()
	fi, err := os.Stat(irPath())
	if err == nil && geoCache != nil && fi.ModTime().Equal(geoStamp) {
		return geoCache, geoInfo
	}
	if err == nil {
		if b, err := os.ReadFile(irPath()); err == nil {
			if ps := ParseList(b); len(ps) >= minRanges {
				geoCache, geoStamp = ps, fi.ModTime()
				geoInfo = GeoInfo{Ranges: len(ps), Updated: fi.ModTime().Unix(), Source: "downloaded"}
				return geoCache, geoInfo
			}
		}
	}
	if geoCache == nil || geoInfo.Source != "bundled" {
		geoCache = ParseList(bundledIR)
		geoInfo = GeoInfo{Ranges: len(geoCache), Source: "bundled"}
		geoStamp = time.Time{}
	}
	return geoCache, geoInfo
}

// ParseList reads one range (or address) per line; anything else is
// skipped.
func ParseList(b []byte) []netip.Prefix {
	var out []netip.Prefix
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p, err := netip.ParsePrefix(line); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(line); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

// NeedsUpdate reports whether the list is older than UpdateEvery.
func NeedsUpdate() bool {
	fi, err := os.Stat(irPath())
	return err != nil || time.Since(fi.ModTime()) > UpdateEvery
}

// UpdateIR downloads a fresh list and keeps it if it looks complete.
func UpdateIR(ctx context.Context) (GeoInfo, error) {
	var errs []error
	for _, src := range irSources {
		b, err := download(ctx, src)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ps := ParseList(b)
		if len(ps) < minRanges {
			errs = append(errs, fmt.Errorf("%s: only %d ranges", src, len(ps)))
			continue
		}
		tmp := irPath() + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return GeoInfo{}, err
		}
		if err := os.Rename(tmp, irPath()); err != nil {
			return GeoInfo{}, err
		}
		_, info := IR()
		return info, nil
	}
	return GeoInfo{}, errors.Join(errs...)
}

func download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}
