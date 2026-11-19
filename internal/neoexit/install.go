package neoexit

// Installing NeoExit from NeoTunnel: its release files are downloaded from
// GitHub (checked against the release's SHA256SUMS) into a cache, and the
// program sets itself up ("neoexit install"). A server that can't reach
// GitHub (often the one in Iran) gets the same files from another of its
// servers, through the cache.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"neotunnel/internal/config"
)

const (
	Repo    = "neoauroraproject/neoexit"
	BinPath = "/usr/local/bin/neoexit"
	// MinVersion is the first NeoExit NeoTunnel can install and manage.
	MinVersion = "v1.4.0"
)

// States of NeoExit on a server.
const (
	StateNone      = "none"      // not installed
	StateInstalled = "installed" // installed, not answering
	StateRunning   = "running"
)

// LocalState is NeoExit's state on this server.
func LocalState() string {
	if Local() != nil {
		return StateRunning
	}
	if _, err := os.Stat(BinPath); err == nil {
		return StateInstalled
	}
	return StateNone
}

var versionRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// ValidVersion reports whether v names a release ("latest" too).
func ValidVersion(v string) bool { return v == "latest" || versionRe.MatchString(v) }

// FileNames are the release files for arch: the program, and the Psiphon
// client it runs (optional: older releases may lack it).
func FileNames(arch string) (prog, psiphon string) {
	return "neoexit-linux-" + arch, "psiphon-linux-" + arch
}

func cacheDir(version string) string { return filepath.Join(config.Dir, "neoexit", version) }

// CachePath is where a cached release file is kept (name must be one of
// FileNames).
func CachePath(version, name string) (string, error) {
	if !versionRe.MatchString(version) || !strings.HasPrefix(name, "neoexit-linux-") && !strings.HasPrefix(name, "psiphon-linux-") || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("bad release file %s %s", version, name)
	}
	return filepath.Join(cacheDir(version), name), nil
}

// Cached reports whether the program for arch of version is in the cache.
func Cached(version, arch string) bool {
	prog, _ := FileNames(arch)
	p, err := CachePath(version, prog)
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Size() > 1<<20
}

var github = &http.Client{Timeout: 4 * time.Minute}

// Latest is the tag of NeoExit's latest release, from where GitHub's
// "latest" page redirects (no API, no rate limit).
func Latest(ctx context.Context) (string, error) {
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://github.com/"+Repo+"/releases/latest", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	tag := path.Base(resp.Header.Get("Location"))
	if !versionRe.MatchString(tag) {
		return "", fmt.Errorf("GitHub did not say which release is the latest (%s)", resp.Status)
	}
	return tag, nil
}

// Fetch downloads NeoExit's release version ("latest" is resolved first)
// for arch into the cache, checking each file against SHA256SUMS. It
// reports the version it has.
func Fetch(ctx context.Context, version, arch string) (string, error) {
	if version == "latest" {
		v, err := Latest(ctx)
		if err != nil {
			return "", err
		}
		version = v
	}
	if !versionRe.MatchString(version) {
		return "", fmt.Errorf("bad version %q", version)
	}
	if Newer(MinVersion, version) {
		return "", fmt.Errorf("NeoExit %s is too old to be managed from NeoTunnel (%s or later)", version, MinVersion)
	}
	if Cached(version, arch) {
		return version, nil
	}
	base := "https://github.com/" + Repo + "/releases/download/" + version + "/"
	sums, err := get(ctx, base+"SHA256SUMS", 1<<16)
	if err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want := parseSums(sums)
	prog, psiphon := FileNames(arch)
	for _, name := range []string{psiphon, prog} { // the program last: its presence marks the cache complete
		sum, ok := want[name]
		if !ok {
			if name == prog {
				return "", fmt.Errorf("release %s has no %s", version, name)
			}
			continue
		}
		b, err := get(ctx, base+name, 256<<20)
		if err != nil {
			if name == prog {
				return "", fmt.Errorf("download %s: %w", name, err)
			}
			continue
		}
		if err := SaveCached(version, name, bytes.NewReader(b), sum); err != nil {
			return "", err
		}
	}
	return version, nil
}

// SaveCached stores a release file in the cache when its SHA-256 is sum.
func SaveCached(version, name string, r io.Reader, sum string) error {
	p, err := CachePath(version, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".dl-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, 256<<20))
	tmp.Close()
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return fmt.Errorf("%s: checksum mismatch", name)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// FileSum is the SHA-256 of a cached file, for sending it on.
func FileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PruneCache keeps only the cached release keep (an install leaves the
// one it used, for passing on to other servers).
func PruneCache(keep string) {
	ents, _ := os.ReadDir(filepath.Join(config.Dir, "neoexit"))
	for _, e := range ents {
		if e.IsDir() && e.Name() != keep {
			os.RemoveAll(filepath.Join(config.Dir, "neoexit", e.Name()))
		}
	}
}

// Installed is what "neoexit install --json" reports.
type Installed struct {
	Version   string `json:"version"`
	User      string `json:"user"`
	Password  string `json:"password,omitempty"` // first install only
	PanelPort int    `json:"panel_port"`
	PanelPath string `json:"panel_path"`
	Proxy     string `json:"proxy"`
	Running   bool   `json:"running"`
}

// Install sets up the cached release version on this server: a first
// install, or an upgrade that keeps the settings.
func Install(ctx context.Context, version string) (Installed, error) {
	var res Installed
	arch := runtime.GOARCH
	if !Cached(version, arch) {
		return res, fmt.Errorf("NeoExit %s for %s isn't downloaded", version, arch)
	}
	prog, psiphon := FileNames(arch)
	pp, _ := CachePath(version, prog)
	args := []string{"install", "--json"}
	if ps, _ := CachePath(version, psiphon); fileOK(ps) {
		args = append(args, "--psiphon", ps)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, pp, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return res, fmt.Errorf("neoexit install: %v %s", err, strings.TrimSpace(errb.String()))
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return res, fmt.Errorf("neoexit install: %w", err)
	}
	Forget()
	PruneCache(version)
	return res, nil
}

// Uninstall removes NeoExit and its settings from this server.
func Uninstall() error {
	if _, err := os.Stat(BinPath); err != nil {
		return errors.New("NeoExit isn't installed here")
	}
	out, err := exec.Command(BinPath, "uninstall").CombinedOutput()
	Forget()
	if err != nil {
		return fmt.Errorf("neoexit uninstall: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileOK(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 1<<20
}

func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := github.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

func parseSums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return out
}

// Newer reports whether version a is newer than b ("v1.4.0" > "v1.3.2").
func Newer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var p [3]int
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "-")
	if n, _ := fmt.Sscanf(v, "%d.%d.%d", &p[0], &p[1], &p[2]); n != 3 {
		return p, false
	}
	return p, true
}

// NewestCached is the newest release in the cache that has the program
// for arch ("" = none): what "latest" falls back to when no server can
// reach GitHub.
func NewestCached(arch string) string {
	best := ""
	ents, _ := os.ReadDir(filepath.Join(config.Dir, "neoexit"))
	for _, e := range ents {
		v := e.Name()
		if e.IsDir() && versionRe.MatchString(v) && Cached(v, arch) && !Newer(MinVersion, v) && (best == "" || Newer(v, best)) {
			best = v
		}
	}
	return best
}
