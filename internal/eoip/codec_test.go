package eoip

import (
	"bytes"
	"testing"
)

func frame(n int) []byte {
	f := make([]byte, n)
	for i := range f {
		f[i] = byte(i * 7)
	}
	return f
}

func encode(f framing, id uint16, fr []byte) []byte {
	p := make([]byte, f.headerLen()+len(fr))
	f.put(p, id, len(fr))
	copy(p[f.headerLen():], fr)
	return p
}

func TestGREWireFormat(t *testing.T) {
	p := encode(framingGRE, 0x1234, frame(60))
	want := []byte{0x20, 0x01, 0x64, 0x00, 0x00, 60, 0x34, 0x12}
	if !bytes.Equal(p[:GREHeaderLen], want) {
		t.Fatalf("header % x, want % x", p[:GREHeaderLen], want)
	}
}

func TestEtherIPWireFormat(t *testing.T) {
	// MikroTik's EoIPv6 tunnel id 1234 (0x4d2) goes out as 43 d2.
	p := encode(framingEtherIP, 1234, frame(60))
	if p[0] != 0x43 || p[1] != 0xd2 {
		t.Fatalf("header % x, want 43 d2", p[:2])
	}
}

func TestRoundTrip(t *testing.T) {
	for _, f := range []framing{framingGRE, framingEtherIP} {
		for _, id := range []uint16{0, 1, 0x0abc, maxEtherIPID} {
			for _, n := range []int{0, 14, 60, 1514, 9000} {
				fr := frame(n)
				gotID, got, err := f.parse(encode(f, id, fr))
				if err != nil || gotID != id || !bytes.Equal(got, fr) {
					t.Fatalf("framing %d id %d len %d: %v %d %d", f, id, n, err, gotID, len(got))
				}
			}
		}
	}
	// GRE's 16-bit id survives in full.
	if id, _, _ := framingGRE.parse(encode(framingGRE, 0xfedc, nil)); id != 0xfedc {
		t.Errorf("gre id %x", id)
	}
}

func TestGRELengthTrimsPadding(t *testing.T) {
	p := append(encode(framingGRE, 7, frame(20)), 0, 0, 0, 0) // trailing padding
	if _, got, err := framingGRE.parse(p); err != nil || len(got) != 20 {
		t.Fatalf("padding not trimmed: %v %d", err, len(got))
	}
}

func TestRejectsForeignPackets(t *testing.T) {
	bad := [][]byte{
		nil,
		{0x20, 0x01, 0x64},                   // short
		{0x00, 0x00, 0x08, 0x00, 0, 0, 0, 0}, // plain GRE (IPv4 payload)
		{0x20, 0x00, 0x64, 0x00, 0, 0, 0, 0}, // wrong version
		{0x20, 0x01, 0x65, 0x58, 0, 0, 0, 0}, // GRETAP ethertype
		{0x20, 0x01, 0x64, 0x00, 0x00, 0x10, 0x01, 0x00, 1}, // length past the end
	}
	for _, p := range bad {
		if _, _, err := framingGRE.parse(p); err == nil {
			t.Errorf("accepted % x", p)
		}
	}
	if _, _, err := framingEtherIP.parse([]byte{0x40, 0x01}); err == nil {
		t.Error("EtherIP with a wrong version accepted")
	}
}
