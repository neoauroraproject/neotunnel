package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"neotunnel/internal/bench"
)

// Tests and benchmarks run in the background; the panel polls their log.
// Only one runs at a time so measurements don't disturb each other.

type job struct {
	mu       sync.Mutex
	ID       string
	Kind     string
	Target   string
	Status   string // running, done, failed, cancelled
	Log      []string
	Results  []bench.Result
	Error    string
	Started  time.Time
	Finished time.Time
	Live     JobLive
	stop     context.CancelFunc
}

// JobLive is the measurement in progress, for the panel's gauge.
type JobLive struct {
	Step  string  `json:"step"`  // what is being measured (latest log line)
	Phase string  `json:"phase"` // ping, upload, download
	Value float64 `json:"value"` // ms for ping, bits/s otherwise
	Peak  float64 `json:"peak"`  // highest value of this phase
	Seq   int     `json:"seq"`   // counts steps, so the panel can tell them apart
}

type jobView struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Target   string         `json:"target"`
	Status   string         `json:"status"`
	Log      []string       `json:"log"`
	Results  []bench.Result `json:"results"`
	Error    string         `json:"error,omitempty"`
	Started  int64          `json:"started"`
	Finished int64          `json:"finished,omitempty"`
	Live     JobLive        `json:"live"`
}

func (j *job) log(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Log = append(j.Log, line)
	if !strings.HasPrefix(line, " ") {
		j.Live.Step = line
		j.Live.Seq++
	}
}

func (j *job) live(phase string, v float64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if phase != j.Live.Phase {
		j.Live.Peak = 0
	}
	j.Live.Phase, j.Live.Value = phase, v
	if v > j.Live.Peak {
		j.Live.Peak = v
	}
}

func (j *job) view() jobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := jobView{
		ID: j.ID, Kind: j.Kind, Target: j.Target, Status: j.Status, Error: j.Error,
		Log: append([]string{}, j.Log...), Results: append([]bench.Result{}, j.Results...),
		Started: j.Started.UnixMilli(), Live: j.Live,
	}
	if !j.Finished.IsZero() {
		v.Finished = j.Finished.UnixMilli()
	}
	return v
}

type jobs struct {
	mu   sync.Mutex
	all  map[string]*job
	busy bool
}

func newJobs() *jobs { return &jobs{all: map[string]*job{}} }

func (js *jobs) start(kind, target string, run func(context.Context, func(string), func(string, float64)) ([]bench.Result, error)) (*job, error) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.busy {
		return nil, errors.New("یک تست دیگر در حال اجراست؛ صبر کنید تا تمام شود")
	}
	b := make([]byte, 8)
	rand.Read(b)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	j := &job{ID: hex.EncodeToString(b), Kind: kind, Target: target, Status: "running", Started: time.Now(), stop: cancel}
	js.all[j.ID] = j
	js.busy = true
	js.prune()
	go func() {
		results, err := run(ctx, j.log, j.live)
		j.mu.Lock()
		j.Results, j.Finished = results, time.Now()
		switch {
		case ctx.Err() == context.Canceled:
			j.Status = "cancelled"
		case err != nil:
			j.Status, j.Error = "failed", err.Error()
		default:
			j.Status = "done"
		}
		j.mu.Unlock()
		cancel()
		js.mu.Lock()
		js.busy = false
		js.mu.Unlock()
	}()
	return j, nil
}

// prune keeps the 20 most recent jobs.
func (js *jobs) prune() {
	if len(js.all) <= 20 {
		return
	}
	list := make([]*job, 0, len(js.all))
	for _, j := range js.all {
		list = append(list, j)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Started.After(list[b].Started) })
	for _, j := range list[20:] {
		delete(js.all, j.ID)
	}
}

func (js *jobs) get(id string) (*job, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	j, ok := js.all[id]
	return j, ok
}

func (js *jobs) cancel(id string) {
	if j, ok := js.get(id); ok {
		j.stop()
	}
}

func (js *jobs) list() []jobView {
	js.mu.Lock()
	list := make([]*job, 0, len(js.all))
	for _, j := range js.all {
		list = append(list, j)
	}
	js.mu.Unlock()
	sort.Slice(list, func(a, b int) bool { return list[a].Started.After(list[b].Started) })
	out := make([]jobView, len(list))
	for i, j := range list {
		out[i] = j.view()
	}
	return out
}
