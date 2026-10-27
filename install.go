// Package neotunnel carries the installer inside the program, so a server
// without GitHub access installs from the release file alone.
package neotunnel

import _ "embed"

//go:embed install.sh
var InstallScript string
