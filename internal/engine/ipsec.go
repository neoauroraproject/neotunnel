package engine

import (
	"encoding/binary"
	"fmt"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
)

// IPsec for kernel tunnels works like MikroTik's ipsec-secret: the tunnel's
// own outer packets (and nothing else) are protected with ESP in transport
// mode. Keys and SPIs are derived from the tunnel secret per direction, so
// there is no key exchange to fail or to block.

// spiBase marks the SPIs NeoTunnel installs.
const spiBase = 0x4e000000

// espSA is one direction of a tunnel's ESP: from side `from` to the other.
type espSA struct {
	SPI  uint32
	Key  []byte
	Algo string
}

func espFor(s LinkSpec, from int) (espSA, error) {
	c, err := config.LookupCipher(s.Encrypt)
	if err != nil {
		return espSA{}, err
	}
	b, err := config.DeriveKey(s.Secret, s.Tunnel, fmt.Sprintf("esp %s %d>%d", c.Name, from, 1-from), 4+c.KeyLen)
	if err != nil {
		return espSA{}, err
	}
	spi := spiBase | binary.BigEndian.Uint32(b[:4])&0x00ffffff
	if spi == spiBase {
		spi++
	}
	return espSA{SPI: spi, Key: b[4:], Algo: c.Algo}, nil
}

// outerProto returns the IP protocol and UDP destination port that a
// tunnel's packets leave this node with.
func outerProto(s LinkSpec) (proto int, port uint16) {
	ki, _ := config.LookupKind(s.Kind)
	if s.FOU || ki.UDP || (ki.PortEncap && s.Port != 0) {
		return 17, s.Port
	}
	return ki.IPProto, 0
}

// protects lists the nftables matches that drop unencrypted packets of
// encrypted tunnels.
func protects(specs []LinkSpec) []forward.Protect {
	var out []forward.Protect
	for _, s := range specs {
		if s.Encrypt == "" {
			continue
		}
		p, port := outerProto(s)
		out = append(out, forward.Protect{Remote: s.Remote, Proto: p, Port: port})
	}
	return out
}
