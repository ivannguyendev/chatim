package command

import (
	"sort"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const codeCount = int(codes.Unauthenticated) + 1

var latencyBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type CommandLatency struct {
	Command string
	Read    func() metrics.Histogram
}

type stats struct {
	names   []string
	counts  []atomic.Uint64
	buckets []atomic.Uint64
	totals  []atomic.Uint64
	nanos   []atomic.Int64
}

func newStats(names []string) *stats {
	return &stats{
		names:   names,
		counts:  make([]atomic.Uint64, len(names)*codeCount),
		buckets: make([]atomic.Uint64, len(names)*len(latencyBounds)),
		totals:  make([]atomic.Uint64, len(names)),
		nanos:   make([]atomic.Int64, len(names)),
	}
}

func (s *stats) count(cmd int, code codes.Code) {
	s.counts[cmd*codeCount+int(code)].Add(1)
}

func (s *stats) observe(cmd int, elapsed time.Duration) {
	elapsed = max(elapsed, 0)
	if i := sort.SearchFloat64s(latencyBounds, elapsed.Seconds()); i < len(latencyBounds) {
		s.buckets[cmd*len(latencyBounds)+i].Add(1)
	}
	s.nanos[cmd].Add(int64(elapsed))
	s.totals[cmd].Add(1)
}

func (s *stats) histogram(cmd int) metrics.Histogram {
	h := metrics.Histogram{Bounds: latencyBounds, Cumulative: make([]uint64, len(latencyBounds))}
	var running uint64
	for i := range latencyBounds {
		running += s.buckets[cmd*len(latencyBounds)+i].Load()
		h.Cumulative[i] = running
	}
	h.Count = max(s.totals[cmd].Load(), running)
	h.Sum = time.Duration(s.nanos[cmd].Load()).Seconds()
	return h
}

func (s *stats) commandSources() []metrics.Source {
	sources := make([]metrics.Source, 0, len(s.counts))
	for cmd, name := range s.names {
		for code := codes.OK; code <= codes.Unauthenticated; code++ {
			counter := &s.counts[cmd*codeCount+int(code)]
			sources = append(sources, metrics.Source{
				Name:   "commands_total",
				Help:   "Client commands handled, by command and result code.",
				Labels: map[string]string{"command": name, "code": code.String()},
				Read:   func() float64 { return float64(counter.Load()) },
			})
		}
	}
	return sources
}

func (s *stats) latencies() []CommandLatency {
	out := make([]CommandLatency, len(s.names))
	for cmd, name := range s.names {
		out[cmd] = CommandLatency{Command: name, Read: func() metrics.Histogram { return s.histogram(cmd) }}
	}
	return out
}
