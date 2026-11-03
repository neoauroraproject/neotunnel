// Package version is set at build time:
//
//	go build -ldflags "-X neotunnel/internal/version.Version=v0.1.0"
package version

import (
	"regexp"
	"runtime"
	"strconv"
)

var Version = "dev"

const (
	Repo       = "neoauroraproject/neotunnel"
	RepoURL    = "https://github.com/" + Repo
	InstallURL = "https://raw.githubusercontent.com/" + Repo + "/main/install.sh"
	// Channel is the Telegram channel for news and releases.
	Channel = "https://t.me/neoaurora"
)

func Arch() string { return runtime.GOARCH }

var tagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Release reports whether v is a release tag ("v0.8.16"), not a dev build.
func Release(v string) bool { return tagRe.MatchString(v) }

// Newer reports whether release a is newer than release b ("v0.8.4" >
// "v0.8.3"). A build that isn't a release ("dev") is never newer, nor older.
func Newer(a, b string) bool {
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
