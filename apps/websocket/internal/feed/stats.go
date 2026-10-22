package feed

import (
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/time/rate"
)

type streamStats struct {
	stream       string
	lag          atomic.Uint64
	pending      atomic.Uint64
	resets       atomic.Uint64
	stale        atomic.Uint64
	serial       atomic.Uint64
	jumps        atomic.Uint64
	jumpFailures atomic.Uint64
	suppressed   atomic.Uint64
	behindSince  atomic.Int64
	suppressLog  rate.Sometimes
}

func newStreamStats(stream string, logEvery time.Duration) *streamStats {
	return &streamStats{stream: stream, suppressLog: rate.Sometimes{Interval: logEvery}}
}

func (s *streamStats) observe(meta *jetstream.MsgMetadata, now time.Time) {
	s.lag.Store(math.Float64bits(max(now.Sub(meta.Timestamp), 0).Seconds()))
	s.pending.Store(meta.NumPending)
	serial := consumerSerial(meta.Consumer)
	if serial == 0 {
		return
	}
	prev := s.serial.Load()
	if serial > prev && s.serial.CompareAndSwap(prev, serial) && prev > 0 {
		s.resets.Add(serial - prev)
	}
}

func (s *streamStats) behindSeconds() float64 {
	since := s.behindSince.Load()
	if since == 0 {
		return 0
	}
	return max(time.Since(time.Unix(0, since)), 0).Seconds()
}

func (s *streamStats) lagSeconds() float64 { return math.Float64frombits(s.lag.Load()) }

func consumerSerial(name string) uint64 {
	_, tail, ok := cutLast(name, "_")
	if !ok {
		return 0
	}
	n, err := strconv.ParseUint(tail, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
