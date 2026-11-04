package engine

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// A server with IPv6 off: the tunnel's interface gets it back on and keeps
// its IPv6 addresses; without IPv6 in the kernel it keeps the IPv4 ones.
func TestUsableAddrs(t *testing.T) {
	old := ipv6Conf
	defer func() { ipv6Conf = old }()
	want := []netip.Prefix{netip.MustParsePrefix("10.77.0.1/30"), netip.MustParsePrefix("fd4e:5700:1::1/64")}

	ipv6Conf = t.TempDir()
	flag := filepath.Join(ipv6Conf, "nt-t1", "disable_ipv6")
	os.MkdirAll(filepath.Dir(flag), 0o755)
	os.WriteFile(flag, []byte("1\n"), 0o644)
	got, err := usableAddrs("nt-t1", want)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if b, _ := os.ReadFile(flag); string(b) != "0" {
		t.Fatalf("disable_ipv6 is %q, want 0", b)
	}

	ipv6Conf = filepath.Join(t.TempDir(), "absent")
	got, err = usableAddrs("nt-t1", want)
	if err == nil || len(got) != 1 || !got[0].Addr().Is4() {
		t.Fatalf("without IPv6: got %v, %v", got, err)
	}
	if got, err := usableAddrs("nt-t1", want[:1]); err != nil || len(got) != 1 {
		t.Fatalf("IPv4 only: got %v, %v", got, err)
	}
}
