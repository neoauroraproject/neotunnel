package engine

import (
	"slices"
	"testing"
)

func TestRxIRQName(t *testing.T) {
	devs := []string{"ens192", "virtio0"}
	for name, want := range map[string]bool{
		"ens192-rxtx-3":    true, // vmxnet3
		"ens192-event-4":   false,
		"ens192-TxRx-1":    true, // Intel
		"ens192-tx-0":      false,
		"ens192-rx-2":      true,
		"virtio0-input.1":  true,
		"virtio0-output.1": false,
		"virtio0-config":   false,
		"ens1920-rx-0":     false, // another card
		"mlx5_comp3":       false,
	} {
		if got := rxIRQName(name, devs); got != want {
			t.Errorf("rxIRQName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSpreadMask(t *testing.T) {
	for _, c := range []struct {
		ncpu int
		irq  []int
		want string
	}{
		{4, []int{3}, "00000007"}, // queue on core 3: cores 0-2 take the rest
		{4, []int{0}, "0000000e"},
		{4, nil, "0000000f"},               // interrupt unknown: every core
		{4, []int{0, 1, 2, 3}, "0000000f"}, // interrupt everywhere: every core
		{1, []int{0}, "0"},
		{40, []int{39}, "0000007f,ffffffff"},
	} {
		if got := spreadMask(c.ncpu, c.irq); !sameMask(got, c.want) {
			t.Errorf("spreadMask(%d, %v) = %s, want %s", c.ncpu, c.irq, got, c.want)
		}
	}
}

func TestParseCPUList(t *testing.T) {
	if got := parseCPUList("0-2,5"); !slices.Equal(got, []int{0, 1, 2, 5}) {
		t.Errorf("parseCPUList = %v", got)
	}
	if got := parseCPUList(""); len(got) != 0 {
		t.Errorf("empty list = %v", got)
	}
}
