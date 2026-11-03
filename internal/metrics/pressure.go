package metrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Where a weak server runs out of CPU for packets. A tunnel's packets are
// handled in softirq (the kernel's packet work), mostly on one core; when
// that core is full the kernel first defers work (time_squeeze) and then
// drops packets its backlog has no room for (dropped). Fragmented packets
// (a wrong MTU) cost much more CPU each. These counters tell apart a CPU
// limit from a datacenter or path limit.

// Jiffies is one cpu line of /proc/stat.
type Jiffies struct{ Busy, Total, Softirq uint64 }

// Stat is one reading of /proc/stat: "cpu" for all cores, "cpuN" for each.
type Stat map[string]Jiffies

// ReadStat reads /proc/stat (false where it doesn't exist).
func ReadStat() (Stat, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, false
	}
	return parseStat(string(b)), true
}

// Usage is how busy the processor was between two readings, in percent.
type Usage struct {
	Total   float64 `json:"total"`   // all cores
	Hottest float64 `json:"hottest"` // the busiest core
	// Softirq is the packet work of all cores; SoftirqHot that of the core
	// with the most of it.
	Softirq    float64 `json:"softirq"`
	SoftirqHot float64 `json:"softirq_hot"`
}

// Between is the usage from reading a to reading b.
func Between(a, b Stat) (Usage, bool) {
	share := func(name string) (busy, soft float64, ok bool) {
		p, ok1 := a[name]
		n, ok2 := b[name]
		if !ok1 || !ok2 || n.Total <= p.Total || n.Busy < p.Busy || n.Softirq < p.Softirq {
			return 0, 0, false
		}
		t := float64(n.Total - p.Total)
		return 100 * float64(n.Busy-p.Busy) / t, 100 * float64(n.Softirq-p.Softirq) / t, true
	}
	var u Usage
	var ok bool
	if u.Total, u.Softirq, ok = share("cpu"); !ok {
		return Usage{}, false
	}
	for name := range b {
		if name == "cpu" {
			continue
		}
		if busy, soft, good := share(name); good {
			u.Hottest = max(u.Hottest, busy)
			u.SoftirqHot = max(u.SoftirqHot, soft)
		}
	}
	return u, true
}

// Softnet is the kernel's packet backlog counters, summed over cores.
type Softnet struct{ Dropped, Squeezed uint64 }

// ReadSoftnet reads /proc/net/softnet_stat: per core, hex columns of
// processed, dropped and time_squeeze.
func ReadSoftnet() (Softnet, bool) {
	b, err := os.ReadFile("/proc/net/softnet_stat")
	if err != nil {
		return Softnet{}, false
	}
	return parseSoftnet(string(b)), true
}

func parseSoftnet(s string) Softnet {
	var n Softnet
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		d, _ := strconv.ParseUint(f[1], 16, 64)
		q, _ := strconv.ParseUint(f[2], 16, 64)
		n.Dropped += d
		n.Squeezed += q
	}
	return n
}

// Frag is the IPv4 fragmentation counters: packets this server cut up and
// fragments it had to put back together.
type Frag struct{ Created, Reassembled uint64 }

// ReadFrag reads the Ip lines of /proc/net/snmp.
func ReadFrag() (Frag, bool) {
	b, err := os.ReadFile("/proc/net/snmp")
	if err != nil {
		return Frag{}, false
	}
	return parseFrag(string(b))
}

func parseFrag(s string) (Frag, bool) {
	var names []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "Ip:" {
			continue
		}
		if names == nil {
			names = f[1:]
			continue
		}
		var fr Frag
		for i, v := range f[1:] {
			if i >= len(names) {
				break
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			switch names[i] {
			case "FragCreates":
				fr.Created = n
			case "ReasmReqds":
				fr.Reassembled = n
			}
		}
		return fr, true
	}
	return Frag{}, false
}

// Proc is a program and its share of the whole processor, in percent.
type Proc struct {
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"`
}

// Procs measures which programs use the CPU, by name (the threads and
// processes of one program add up), between two reads of /proc.
type Procs struct {
	prev map[int]procTicks
	at   time.Time
}

type procTicks struct {
	name  string
	ticks uint64
}

// clockTicks is USER_HZ, 100 on every Linux the program runs on.
const clockTicks = 100

// Read returns the busiest programs since the last Read, at most n.
func (p *Procs) Read(n, cpus int) []Proc {
	cur := map[int]procTicks{}
	dirs, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, d := range dirs {
		b, err := os.ReadFile(d)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(d)))
		if t, ok := parseProcStat(string(b)); ok {
			cur[pid] = t
		}
	}
	now := time.Now()
	prev, at := p.prev, p.at
	p.prev, p.at = cur, now
	if prev == nil || cpus <= 0 {
		return nil
	}
	sec := now.Sub(at).Seconds()
	if sec <= 0 {
		return nil
	}
	by := map[string]uint64{}
	for pid, t := range cur {
		if o, ok := prev[pid]; ok && o.name == t.name && t.ticks >= o.ticks {
			by[t.name] += t.ticks - o.ticks
		}
	}
	var out []Proc
	for name, d := range by {
		if pct := 100 * float64(d) / clockTicks / sec / float64(cpus); pct >= 0.5 {
			out = append(out, Proc{name, pct})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// parseProcStat reads a process's name and its user and system ticks from
// /proc/<pid>/stat; the name sits in parentheses and may hold spaces.
func parseProcStat(s string) (procTicks, bool) {
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < l {
		return procTicks{}, false
	}
	f := strings.Fields(s[r+1:])
	// After the name: state(0) ... utime(11) stime(12).
	if len(f) < 13 {
		return procTicks{}, false
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	return procTicks{s[l+1 : r], u + st}, true
}

// Pressure is the latest reading of where the CPU goes, for the panel.
type Pressure struct {
	At    int64 `json:"at"` // unix seconds; 0 = no reading yet
	Usage Usage `json:"usage"`
	// Drops and Squeezes are per second: packets the kernel dropped for
	// lack of CPU, and times its packet work ran out of budget.
	Drops    float64 `json:"drops"`
	Squeezes float64 `json:"squeezes"`
	// Frags are fragments made and reassembled per second.
	Frags float64 `json:"frags"`
	Procs []Proc  `json:"procs,omitempty"`
	// Conntrack is how many connections the kernel tracks, of at most
	// ConntrackMax (0 when tracking is off); a full table drops new ones.
	Conntrack    int `json:"conntrack,omitempty"`
	ConntrackMax int `json:"conntrack_max,omitempty"`
}

// readConntrack reads the connection tracking table's use and size.
func readConntrack() (n, max int) {
	read := func(f string) int {
		b, _ := os.ReadFile("/proc/sys/net/netfilter/" + f)
		v, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return v
	}
	return read("nf_conntrack_count"), read("nf_conntrack_max")
}

// Sampler takes the Pressure readings between rounds.
type Sampler struct {
	stat  Stat
	net   Softnet
	frag  Frag
	at    time.Time
	procs Procs
}

// Sample reads everything and returns the change since the last Sample
// (ok is false on the first).
func (s *Sampler) Sample(cpus int) (Pressure, bool) {
	now := time.Now()
	st, ok1 := ReadStat()
	sn, ok2 := ReadSoftnet()
	fr, ok3 := ReadFrag()
	procs := s.procs.Read(4, cpus)
	prevStat, prevNet, prevFrag, at := s.stat, s.net, s.frag, s.at
	s.stat, s.net, s.frag, s.at = st, sn, fr, now
	if !ok1 || prevStat == nil {
		return Pressure{}, false
	}
	u, ok := Between(prevStat, st)
	if !ok {
		return Pressure{}, false
	}
	p := Pressure{At: now.Unix(), Usage: u, Procs: procs}
	p.Conntrack, p.ConntrackMax = readConntrack()
	if sec := now.Sub(at).Seconds(); sec > 0 {
		if ok2 && sn.Dropped >= prevNet.Dropped && sn.Squeezed >= prevNet.Squeezed {
			p.Drops = float64(sn.Dropped-prevNet.Dropped) / sec
			p.Squeezes = float64(sn.Squeezed-prevNet.Squeezed) / sec
		}
		if ok3 && fr.Created >= prevFrag.Created && fr.Reassembled >= prevFrag.Reassembled {
			p.Frags = float64(fr.Created-prevFrag.Created+fr.Reassembled-prevFrag.Reassembled) / sec
		}
	}
	return p, true
}
