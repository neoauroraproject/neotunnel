package engine

import (
	"bytes"
	"testing"
)

// The reserved half of the generic netlink header must be zero: kernel 7.0
// rejects SET_DEVICE for WireGuard otherwise.
func TestGenlHeaderReservedZero(t *testing.T) {
	h := genlHeader{cmd: wgCmdSetDevice, version: 1}
	if got := h.Serialize(); !bytes.Equal(got, []byte{wgCmdSetDevice, 1, 0, 0}) || h.Len() != len(got) {
		t.Fatalf("header = % x (len %d)", got, h.Len())
	}
}
