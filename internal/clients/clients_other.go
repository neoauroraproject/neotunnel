//go:build !linux

package clients

// Of reports nothing off Linux: the connections are the kernel's.
func Of(ports map[string]Port) map[string]Report {
	out := map[string]Report{}
	for k := range ports {
		out[k] = Report{}
	}
	return out
}
