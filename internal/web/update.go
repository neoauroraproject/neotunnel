package web

// Updating this server from the panel: the newest release is looked up on
// GitHub (cached, so opening the panel doesn't ask GitHub every time), and
// an update runs the official installer, the same one `neotunnel update`
// runs. The installer restarts this service, so it runs as a transient
// systemd unit of its own: stopping the service kills everything in its
// cgroup, and the installer must outlive that. The panel then waits for
// the new version to answer and reloads.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/version"
)

const (
	releaseAPI   = "https://api.github.com/repos/" + version.Repo + "/releases/latest"
	releaseTTL   = 30 * time.Minute
	releaseRetry = 2 * time.Minute // after a failed check
	// updateLog keeps the installer's output, for the panel when an update
	// doesn't come back.
	updateLog = "/var/log/neotunnel-update.log"
)

// Release is what the panel shows about updates.
type Release struct {
	Current  string `json:"current"`
	Latest   string `json:"latest,omitempty"`
	Newer    bool   `json:"newer"`
	URL      string `json:"url,omitempty"`   // the release page
	Notes    string `json:"notes,omitempty"` // its description (Markdown)
	Checked  int64  `json:"checked,omitempty"`
	Error    string `json:"error,omitempty"`
	Updating string `json:"updating,omitempty"` // the version being installed
	Log      string `json:"log,omitempty"`      // the installer's last lines
}

type updater struct {
	mu       sync.Mutex
	rel      Release
	at       time.Time
	updating string
	started  time.Time
	unit     string
}

func (s *Server) updateRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/update", s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.up.check(r.Context(), r.URL.Query().Get("force") == "1"))
	}))
	m.HandleFunc("POST /api/update", s.authed(func(w http.ResponseWriter, r *http.Request) {
		rel, err := s.up.start(r.Context())
		if err == nil {
			s.handOffSessions()
		}
		reply(w, rel, err)
	}))
}

// The panel's sessions live in memory, so the restart an update causes
// would sign everyone out halfway. Before it, the open sessions are written
// (as hashes; the cookie stays the only copy of a session id) for the next
// process, which reads the file once at start and deletes it.
var handoffFile = filepath.Join(config.Dir, "sessions.handoff")

const handoffTTL = 15 * time.Minute

type handoff struct {
	Written  int64            `json:"written"`
	Sessions map[string]int64 `json:"sessions"` // sha256(id) → expiry (unix)
}

func sessionHash(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

func (s *Server) handOffSessions() {
	s.mu.Lock()
	h := handoff{Written: time.Now().Unix(), Sessions: map[string]int64{}}
	for id, exp := range s.sessions {
		if time.Now().Before(exp) {
			h.Sessions[sessionHash(id)] = exp.Unix()
		}
	}
	s.mu.Unlock()
	if b, err := json.Marshal(h); err == nil {
		os.WriteFile(handoffFile, b, 0o600)
	}
}

// takeHandoff returns the sessions a previous process handed over, if it
// did so recently, and removes the file.
func takeHandoff() map[string]time.Time {
	b, err := os.ReadFile(handoffFile)
	if err != nil {
		return nil
	}
	os.Remove(handoffFile)
	var h handoff
	if json.Unmarshal(b, &h) != nil || time.Since(time.Unix(h.Written, 0)) > handoffTTL {
		return nil
	}
	out := map[string]time.Time{}
	for k, exp := range h.Sessions {
		out[k] = time.Unix(exp, 0)
	}
	return out
}

func (u *updater) check(ctx context.Context, force bool) Release {
	u.mu.Lock()
	defer u.mu.Unlock()
	ttl := releaseTTL
	if u.rel.Error != "" {
		ttl = releaseRetry
	}
	if force || u.at.IsZero() || time.Since(u.at) > ttl {
		u.rel = fetchRelease(ctx)
		u.at = time.Now()
	}
	rel := u.rel
	rel.Current = version.Version
	rel.Newer = rel.Latest != "" && newer(rel.Latest, rel.Current)
	if u.updating != "" {
		if unitActive(u.unit) || time.Since(u.started) < 5*time.Second {
			rel.Updating = u.updating
		} else {
			// The installer finished without replacing this process (it
			// failed, or the version was the same): say what it printed.
			rel.Log = tail(updateLog, 20)
			u.updating = ""
		}
	}
	return rel
}

func fetchRelease(ctx context.Context) Release {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", releaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "neotunnel/"+version.Version)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{Error: "گیت‌هاب از این سرور در دسترس نیست"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{Error: fmt.Sprintf("گیت‌هاب جواب داد: %s", res.Status)}
	}
	var gh struct {
		Tag  string `json:"tag_name"`
		URL  string `json:"html_url"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(res.Body).Decode(&gh); err != nil || !validTag(gh.Tag) {
		return Release{Error: "جواب گیت‌هاب قابل خواندن نبود"}
	}
	return Release{Latest: gh.Tag, URL: gh.URL, Notes: gh.Body, Checked: time.Now().Unix()}
}

// start runs the installer for the newest release, detached from this
// service.
func (u *updater) start(ctx context.Context) (Release, error) {
	rel := u.check(ctx, true)
	if rel.Error != "" {
		return rel, errors.New(rel.Error)
	}
	if rel.Updating != "" {
		return rel, nil
	}
	if !rel.Newer {
		return rel, fmt.Errorf("این سرور همین حالا نسخه‌ی %s را دارد", rel.Current)
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return rel, errors.New("systemd-run روی این سرور نیست؛ از ترمینال به‌روز کنید: sudo neotunnel update")
	}
	unit := "neotunnel-update-" + strconv.FormatInt(time.Now().Unix(), 10)
	script := fmt.Sprintf("set -o pipefail; { date; curl -fsSL %s | bash -s -- --no-menu --version %s; } >%s 2>&1",
		version.InstallURL, rel.Latest, updateLog)
	out, err := exec.Command("systemd-run", "--unit="+unit, "--collect", "--quiet",
		"--description=NeoTunnel update to "+rel.Latest, "/bin/bash", "-c", script).CombinedOutput()
	if err != nil {
		return rel, fmt.Errorf("به‌روزرسانی شروع نشد: %s", strings.TrimSpace(string(out)))
	}
	u.mu.Lock()
	u.updating, u.started, u.unit = rel.Latest, time.Now(), unit
	u.mu.Unlock()
	rel.Updating = rel.Latest
	return rel, nil
}

func unitActive(unit string) bool {
	if unit == "" {
		return false
	}
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	s := strings.TrimSpace(string(out))
	return s == "active" || s == "activating"
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

var tagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func validTag(t string) bool { return tagRe.MatchString(t) }

// newer reports whether release a is newer than b ("v0.8.4" > "v0.8.3"). A
// build that isn't a release ("dev") is never offered an update.
func newer(a, b string) bool {
	pa, pb := tagRe.FindStringSubmatch(a), tagRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return false
}
