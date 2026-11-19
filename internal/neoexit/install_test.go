package neoexit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"neotunnel/internal/config"
)

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{{"v1.4.0", "v1.3.9", true}, {"v1.4.0", "v1.4.0", false}, {"v1.10.0", "v1.9.9", true}, {"v1.3.0", "v1.4.0-dev", false}, {"junk", "v1.0.0", false}} {
		if got := Newer(c.a, c.b); got != c.newer {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
	if !ValidVersion("latest") || !ValidVersion("v1.4.0") || ValidVersion("1.4.0") || ValidVersion("v1.4.0;rm") {
		t.Error("ValidVersion")
	}
	sums := parseSums([]byte("abc  neoexit-linux-amd64\ndef *psiphon-linux-amd64\nbroken\n"))
	if sums["neoexit-linux-amd64"] != "abc" || sums["psiphon-linux-amd64"] != "def" || len(sums) != 2 {
		t.Errorf("sums %v", sums)
	}
}

func TestCache(t *testing.T) {
	dir := t.TempDir()
	old := config.Dir
	config.Dir = dir
	defer func() { config.Dir = old }()

	for _, bad := range [][2]string{{"v1.4.0", "../x"}, {"v1.4.0", "other"}, {"latest", "neoexit-linux-amd64"}, {"../v1", "neoexit-linux-amd64"}} {
		if _, err := CachePath(bad[0], bad[1]); err == nil {
			t.Errorf("CachePath accepted %v", bad)
		}
	}
	data := bytes.Repeat([]byte("x"), 2<<20)
	sum := sha256.Sum256(data)
	if err := SaveCached("v1.4.0", "neoexit-linux-amd64", bytes.NewReader(data), "00"); err == nil {
		t.Error("saved a file with the wrong checksum")
	}
	if Cached("v1.4.0", "amd64") {
		t.Error("cached after a bad checksum")
	}
	if err := SaveCached("v1.4.0", "neoexit-linux-amd64", bytes.NewReader(data), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if !Cached("v1.4.0", "amd64") || Cached("v1.4.0", "arm64") {
		t.Error("Cached")
	}
	// Older than NeoTunnel can manage, or without the program: never picked.
	os.MkdirAll(filepath.Join(dir, "neoexit", "v1.3.0"), 0o700)
	os.WriteFile(filepath.Join(dir, "neoexit", "v1.3.0", "neoexit-linux-amd64"), data, 0o755)
	os.MkdirAll(filepath.Join(dir, "neoexit", "v1.5.0"), 0o700)
	if v := NewestCached("amd64"); v != "v1.4.0" {
		t.Errorf("NewestCached = %q", v)
	}
	PruneCache("v1.4.0")
	if _, err := os.Stat(filepath.Join(dir, "neoexit", "v1.3.0")); !os.IsNotExist(err) {
		t.Error("PruneCache kept an older release")
	}
	if !Cached("v1.4.0", "amd64") {
		t.Error("PruneCache removed the one to keep")
	}
}
