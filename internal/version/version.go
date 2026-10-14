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
)

func Arch() string { return runtime.GOARCH }
