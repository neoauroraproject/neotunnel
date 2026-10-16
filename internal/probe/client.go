package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type LatencyResult struct {
	Sent, Received int
	Min, Avg, Max  time.Duration
	P95            time.Duration
	Jitter         time.Duration // mean difference between consecutive RTTs
}

func (r LatencyResult) Loss() float64 {
	if r.Sent == 0 {
		return 0
	}
	return 100 * float64(r.Sent-r.Received) / float64(r.Sent)
}

// UDP packet: token(16) | seq(4) | sent unix-nanos(8) | padding
const udpHeader = tokenLen + 12

// Latency sends count probes of size bytes, interval apart, and waits up to
// timeout after the last one for stragglers.
func Latency(ctx context.Context, addr string, tok Token, count int, interval time.Duration, size int) (LatencyResult, error) {
	if size < udpHeader {
		size = udpHeader
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		return LatencyResult{}, err
	}
	defer c.Close()

	rtts := make([]time.Duration, count)
	got := make([]bool, count)
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65536)
		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			if n < udpHeader {
				continue
			}
			seq := int(binary.BigEndian.Uint32(buf[tokenLen:]))
			sent := int64(binary.BigEndian.Uint64(buf[tokenLen+4:]))
			if seq >= 0 && seq < count {
				mu.Lock()
				if !got[seq] {
					got[seq] = true
					rtts[seq] = time.Duration(time.Now().UnixNano() - sent)
				}
				mu.Unlock()
			}
		}
	}()

	pkt := make([]byte, size)
	copy(pkt, tok[:])
	t := time.NewTicker(interval)
	defer t.Stop()
	sent := 0
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint32(pkt[tokenLen:], uint32(i))
		binary.BigEndian.PutUint64(pkt[tokenLen+4:], uint64(time.Now().UnixNano()))
		if _, err := c.Write(pkt); err == nil {
			sent++
		}
		select {
		case <-ctx.Done():
			c.Close()
			<-done
			return LatencyResult{}, ctx.Err()
		case <-t.C:
		}
	}
	// Wait for stragglers: at least 1s, or 3x the slowest RTT seen.
	wait := time.Second
	mu.Lock()
	for i, ok := range got {
		if ok && 3*rtts[i] > wait {
			wait = 3 * rtts[i]
		}
	}
	mu.Unlock()
	time.Sleep(min(wait, 3*time.Second))
	c.Close()
	<-done

	r := LatencyResult{Sent: sent}
	var seq []time.Duration
	for i := range got {
		if got[i] {
			seq = append(seq, rtts[i])
		}
	}
	r.Received = len(seq)
	if r.Received == 0 {
		return r, fmt.Errorf("no replies from %s (tunnel down or port blocked)", addr)
	}
	var sum, jsum time.Duration
	for i, d := range seq {
		sum += d
		if i > 0 {
			jsum += time.Duration(math.Abs(float64(d - seq[i-1])))
		}
	}
	r.Avg = sum / time.Duration(len(seq))
	if len(seq) > 1 {
		r.Jitter = jsum / time.Duration(len(seq)-1)
	}
	sorted := append([]time.Duration(nil), seq...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	r.Min, r.Max = sorted[0], sorted[len(sorted)-1]
	r.P95 = sorted[int(math.Ceil(0.95*float64(len(sorted))))-1]
	return r, nil
}

type Direction byte

const (
	Upload   Direction = 'U' // this node -> peer
	Download Direction = 'D' // peer -> this node
)

// Bandwidth runs streams parallel TCP connections for d and returns bits/s.
func Bandwidth(ctx context.Context, addr string, tok Token, dir Direction, d time.Duration, streams int) (float64, error) {
	return BandwidthLive(ctx, addr, tok, dir, d, streams, nil)
}

// BandwidthLive is Bandwidth that also reports the current rate (bits/s,
// measured locally over the last half second) while it runs.
func BandwidthLive(ctx context.Context, addr string, tok Token, dir Direction, d time.Duration, streams int, report func(bps float64)) (float64, error) {
	if streams < 1 {
		streams = 1
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int64
		first error
		moved atomic.Int64
	)
	if report != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			last, at := int64(0), time.Now()
			for {
				select {
				case <-stop:
					return
				case now := <-t.C:
					n := moved.Load()
					report(float64(n-last) * 8 / now.Sub(at).Seconds())
					last, at = n, now
				}
			}
		}()
	}
	start := time.Now()
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := stream(ctx, addr, tok, dir, d, &moved)
			mu.Lock()
			defer mu.Unlock()
			total += n
			if err != nil && first == nil {
				first = err
			}
		}()
	}
	wg.Wait()
	if total == 0 {
		if first == nil {
			first = fmt.Errorf("no data transferred")
		}
		return 0, first
	}
	return float64(total*8) / time.Since(start).Seconds(), nil
}

func stream(ctx context.Context, addr string, tok Token, dir Direction, d time.Duration, moved *atomic.Int64) (int64, error) {
	var dl net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, err := dl.DialContext(dctx, "tcp", addr)
	cancel()
	if err != nil {
		return 0, err
	}
	defer c.Close()
	go func() { <-ctx.Done(); c.Close() }()

	hdr := make([]byte, tokenLen+5)
	copy(hdr, tok[:])
	hdr[tokenLen] = byte(dir)
	binary.BigEndian.PutUint32(hdr[tokenLen+1:], uint32(d.Milliseconds()))
	if _, err := c.Write(hdr); err != nil {
		return 0, err
	}
	switch dir {
	case Upload:
		buf := make([]byte, 128<<10)
		rand.Read(buf)
		deadline := time.Now().Add(d)
		c.SetWriteDeadline(deadline.Add(2 * time.Second))
		for time.Now().Before(deadline) {
			n, err := c.Write(buf)
			moved.Add(int64(n))
			if err != nil {
				break
			}
		}
		c.(*net.TCPConn).CloseWrite()
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		var out [8]byte
		if _, err := io.ReadFull(c, out[:]); err != nil {
			return 0, fmt.Errorf("upload result: %w", err)
		}
		return int64(binary.BigEndian.Uint64(out[:])), nil
	default:
		c.SetReadDeadline(time.Now().Add(d + 5*time.Second))
		buf := make([]byte, 128<<10)
		var n int64
		for {
			m, err := c.Read(buf)
			n += int64(m)
			moved.Add(int64(m))
			if err != nil {
				if n > 0 || err == io.EOF {
					err = nil
				}
				return n, err
			}
		}
	}
}
