package forward

import (
	"os"
	"testing"
)

// dumpTo is where a test writes its script for a check with a real nft
// (NT_DUMP), "" for none.
func dumpTo() string { return os.Getenv("NT_DUMP") }

func writeFile(t *testing.T, p, s string) {
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
