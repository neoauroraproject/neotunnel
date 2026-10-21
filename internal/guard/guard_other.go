//go:build !linux

package guard

func Sockets() []Socket                { return nil }
func SSHPorts() []uint16               { return []uint16{22} }
func PublicIfaces(string) []string     { return nil }
func Apply(Input) error                { return nil }
func Remove() error                    { return nil }
func Dropped() (packets, bytes uint64) { return 0, 0 }
