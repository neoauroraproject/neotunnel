// Package mtr traces the path to an address the way mtr does: rounds of
// ICMP echo requests with a rising TTL, each hop's loss and round-trip
// times from the replies. Between Iran and abroad the path one way is
// often not the path back, so the panel runs it from both servers.
package mtr

import (
	"math"
	"net/netip"
	"sort"
)

// Hop is one step of the path.
type Hop struct {
	TTL int `json:"ttl"`
	// Host is the address that answered most at this TTL ("" = none did).
	Host string `json:"host,omitempty"`
	// Others are other addresses that answered too (a path that changes,
	// or load balancing).
	Others []string `json:"others,omitempty"`
	Sent   int      `json:"sent"`
	Got    int      `json:"got"`
	// Loss is in percent; times in milliseconds.
	Loss  float64 `json:"loss"`
	Last  float64 `json:"last"`
	Avg   float64 `json:"avg"`
	Best  float64 `json:"best"`
	Worst float64 `json:"worst"`
	StDev float64 `json:"stdev"`
	// IR says Host is in Iran (filled in by the caller).
	IR bool `json:"ir,omitempty"`
}

// Result is a trace to Target.
type Result struct {
	Target string `json:"target"`
	Hops   []Hop  `json:"hops"`
	// Reached says the target itself answered.
	Reached bool `json:"reached"`
	Rounds  int  `json:"rounds"`
}

// Limits on a trace.
const (
	MaxHops   = 30
	MaxRounds = 60
)

// reply is one answer to a probe.
type reply struct {
	ttl  int
	from netip.Addr
	rtt  float64 // ms
	echo bool    // the target's own echo reply
}

// summarize turns the probes sent per TTL and the replies into hops, up to
// the first TTL the target answered at.
func summarize(target netip.Addr, rounds int, sent map[int]int, replies []reply) Result {
	r := Result{Target: target.String(), Rounds: rounds}
	last := 0
	for ttl := range sent {
		last = max(last, ttl)
	}
	for _, x := range replies {
		if x.echo && (!r.Reached || x.ttl < last) {
			r.Reached, last = true, x.ttl
		}
	}
	for ttl := 1; ttl <= last; ttl++ {
		h := Hop{TTL: ttl, Sent: sent[ttl]}
		count := map[netip.Addr]int{}
		var rtts []float64
		for _, x := range replies {
			if x.ttl != ttl {
				continue
			}
			count[x.from]++
			rtts = append(rtts, x.rtt)
			h.Last = x.rtt
		}
		h.Got = min(len(rtts), h.Sent)
		if h.Sent > 0 {
			h.Loss = math.Round(float64(h.Sent-h.Got)/float64(h.Sent)*1000) / 10
		}
		var hosts []netip.Addr
		for a := range count {
			hosts = append(hosts, a)
		}
		sort.Slice(hosts, func(i, j int) bool {
			if count[hosts[i]] != count[hosts[j]] {
				return count[hosts[i]] > count[hosts[j]]
			}
			return hosts[i].Less(hosts[j])
		})
		for i, a := range hosts {
			if i == 0 {
				h.Host = a.String()
			} else {
				h.Others = append(h.Others, a.String())
			}
		}
		if len(rtts) > 0 {
			h.Best, h.Worst = rtts[0], rtts[0]
			sum := 0.0
			for _, v := range rtts {
				sum += v
				h.Best, h.Worst = min(h.Best, v), max(h.Worst, v)
			}
			h.Avg = sum / float64(len(rtts))
			for _, v := range rtts {
				h.StDev += (v - h.Avg) * (v - h.Avg)
			}
			h.StDev = math.Sqrt(h.StDev / float64(len(rtts)))
			for _, p := range []*float64{&h.Last, &h.Avg, &h.Best, &h.Worst, &h.StDev} {
				*p = math.Round(*p*10) / 10
			}
		}
		r.Hops = append(r.Hops, h)
	}
	return r
}
