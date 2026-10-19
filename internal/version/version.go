// Package version is set at build time:
//
//	go build -ldflags "-X neotunnel/internal/version.Version=v0.1.0"
package version

import "runtime"

var Version = "dev"

const (
	Repo       = "neoauroraproject/neotunnel"
	RepoURL    = "https://github.com/" + Repo
	InstallURL = "https://raw.githubusercontent.com/" + Repo + "/main/install.sh"
	// Channel is the Telegram channel for news and releases.
	Channel = "https://t.me/neoaurora"
)

func Arch() string { return runtime.GOARCH }
