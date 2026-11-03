package mtr

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Run traces the path to target in rounds a second apart (1 to MaxRounds).
func Run(ctx context.Context, target netip.Addr, rounds int) (Result, error) {
	target = target.Unmap()
	rounds = max(1, min(rounds, MaxRounds))
	v6 := target.Is6()
	fam, proto := unix.AF_INET, unix.IPPROTO_ICMP
	if v6 {
		fam, proto = unix.AF_INET6, unix.IPPROTO_ICMPV6
	}
	fd, err := unix.Socket(fam, unix.SOCK_RAW|unix.SOCK_CLOEXEC, proto)
	if err != nil {
		return Result{}, fmt.Errorf("raw ICMP socket (needs root): %w", err)
	}
	defer unix.Close(fd)
	if v6 {
		// Only what answers probes: echo replies, time exceeded, unreachable.
		var f unix.ICMPv6Filter
		for i := range f.Data {
			f.Data[i] = 0xffffffff
		}
		for _, t := range []int{1, 3, 129} {
			f.Data[t>>5] &^= 1 << (uint(t) & 31)
		}
		unix.SetsockoptICMPv6Filter(fd, unix.SOL_ICMPV6, unix.ICMPV6_FILTER, &f)
	}
	unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 200_000})
	id := uint16(rand.Uint32())

	var mu sync.Mutex
	sentAt := map[uint16]time.Time{} // by seq
	ttlOf := map[uint16]int{}
	sent := map[int]int{}
	var replies []reply
	reachedAt := 0 // the lowest TTL the target answered at

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			n, from, err := unix.Recvfrom(fd, buf, 0)
			now := time.Now()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if err == unix.EAGAIN || err == unix.EINTR {
					mu.Lock()
					stop := sentAt == nil
					mu.Unlock()
					if stop {
						return
					}
					continue
				}
				return
			}
			src, ok := sockAddr(from)
			if !ok {
				continue
			}
			seq, echo, ok := parse(buf[:n], v6, id)
			if !ok {
				continue
			}
			mu.Lock()
			if at, ok := sentAt[seq]; ok {
				delete(sentAt, seq)
				ttl := ttlOf[seq]
				replies = append(replies, reply{ttl: ttl, from: src, rtt: float64(now.Sub(at).Microseconds()) / 1000, echo: echo && src == target})
				if echo && src == target && (reachedAt == 0 || ttl < reachedAt) {
					reachedAt = ttl
				}
			}
			mu.Unlock()
		}
	}()

	dst := sockaddrOf(target)
	seq := uint16(0)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for r := 0; r < rounds; r++ {
		mu.Lock()
		top := MaxHops
		if reachedAt > 0 {
			top = reachedAt
		}
		mu.Unlock()
		for ttl := 1; ttl <= top; ttl++ {
			seq++
			if v6 {
				unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, ttl)
			} else {
				unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TTL, ttl)
			}
			mu.Lock()
			sentAt[seq], ttlOf[seq] = time.Now(), ttl
			sent[ttl]++
			mu.Unlock()
			if err := unix.Sendto(fd, echoRequest(v6, id, seq), 0, dst); err != nil {
				mu.Lock()
				delete(sentAt, seq)
				sent[ttl]--
				mu.Unlock()
			}
			time.Sleep(3 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			r = rounds
		case <-t.C:
		}
	}
	// The last round's late replies.
	select {
	case <-ctx.Done():
	case <-time.After(1500 * time.Millisecond):
	}
	mu.Lock()
	sentAt = nil
	mu.Unlock()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if reachedAt > 0 {
		for ttl := range sent {
			if ttl > reachedAt {
				delete(sent, ttl)
			}
		}
	}
	return summarize(target, rounds, sent, replies), ctx.Err()
}

func sockaddrOf(a netip.Addr) unix.Sockaddr {
	if a.Is4() {
		return &unix.SockaddrInet4{Addr: a.As4()}
	}
	return &unix.SockaddrInet6{Addr: a.As16()}
}

func sockAddr(sa unix.Sockaddr) (netip.Addr, bool) {
	switch s := sa.(type) {
	case *unix.SockaddrInet4:
		return netip.AddrFrom4(s.Addr), true
	case *unix.SockaddrInet6:
		return netip.AddrFrom16(s.Addr).Unmap(), true
	}
	return netip.Addr{}, false
}

// echoRequest is an ICMP (or ICMPv6) echo request with a few bytes of data.
func echoRequest(v6 bool, id, seq uint16) []byte {
	b := make([]byte, 8+24)
	b[0] = 8
	if v6 {
		b[0] = 128 // the kernel fills in ICMPv6's checksum
	}
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	copy(b[8:], "neotunnel-mtr-probe-0000")
	if !v6 {
		binary.BigEndian.PutUint16(b[2:], checksum(b))
	}
	return b
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}
