package tcptun

// flowHash maps a packet to its flow (addresses, protocol and, for TCP and
// UDP, ports), so every packet of a flow takes the same connection.
func flowHash(p []byte) uint32 {
	const prime = 16777619
	h := uint32(2166136261)
	mix := func(b []byte) {
		for _, c := range b {
			h = (h ^ uint32(c)) * prime
		}
	}
	if len(p) < 1 {
		return h
	}
	switch p[0] >> 4 {
	case 4:
		if len(p) < 20 {
			return h
		}
		ihl := int(p[0]&0x0f) * 4
		proto := p[9]
		mix(p[9:10])
		mix(p[12:20])
		fragment := p[6]&0x3f != 0 || p[7] != 0
		if !fragment && (proto == 6 || proto == 17) && len(p) >= ihl+4 {
			mix(p[ihl : ihl+4])
		}
	case 6:
		if len(p) < 40 {
			return h
		}
		next := p[6]
		mix(p[6:7])
		mix(p[8:40])
		if (next == 6 || next == 17) && len(p) >= 44 {
			mix(p[40:44])
		}
	}
	return h
}
