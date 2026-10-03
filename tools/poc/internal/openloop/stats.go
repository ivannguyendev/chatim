package openloop

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type Result struct {
	Shot     Shot
	Done     time.Time
	Code     codes.Code
	Attempts int
}

type Stats struct {
	mu     sync.Mutex
	ack    latency.Recorder
	totals Sends
}

type Sends struct {
	Acked       int
	Failed      map[codes.Code]int
	Attempts    int
	Retried     int
	MaxAttempts int
	Ack         latency.Summary
}

type Report struct {
	Plan   Plan
	Counts Counts
	Sends  Sends
}

func (s *Stats) Record(r Result) {
	if !r.Shot.Measured {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &s.totals
	t.Attempts += r.Attempts
	t.MaxAttempts = max(t.MaxAttempts, r.Attempts)
	if r.Attempts > 1 {
		t.Retried++
	}
	if r.Code == codes.OK {
		t.Acked++
		s.ack.Add(r.Done.Sub(r.Shot.Scheduled))
		return
	}
	if t.Failed == nil {
		t.Failed = map[codes.Code]int{}
	}
	t.Failed[r.Code]++
}

func (s *Stats) Sends() Sends {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.totals
	out.Failed = maps.Clone(s.totals.Failed)
	out.Ack = s.ack.Summary()
	return out
}

func (s Sends) FailedTotal() int {
	n := 0
	for _, v := range s.Failed {
		n += v
	}
	return n
}

func (r Report) Write(w io.Writer) {
	secs := r.Plan.Duration.Seconds()
	m, all, sends := r.Counts.Measured, r.Counts.All, r.Sends
	fmt.Fprintf(w, "rate target=%d/s sent=%.0f/s acked=%.0f/s over the %v measured window (after %v warmup)\n",
		r.Plan.Rate, float64(m.Issued)/secs, float64(sends.Acked)/secs, r.Plan.Duration, r.Plan.Warmup)
	fmt.Fprintf(w, "sends due=%d sent=%d acked=%d failed=%d shed_by_client=%d (max in-flight %d)\n",
		m.Due, m.Issued, sends.Acked, sends.FailedTotal(), m.Shed, r.Plan.MaxInflight)
	fmt.Fprintf(w, "failed by code: %s\n", byCode(sends.Failed))
	fmt.Fprintf(w, "retried sends=%d attempts=%d max attempts=%d\n", sends.Retried, sends.Attempts, sends.MaxAttempts)
	fmt.Fprintf(w, "with warmup: due=%d sent=%d shed_by_client=%d\n", all.Due, all.Issued, all.Shed)
	fmt.Fprintf(w, "pacer lag (issue minus scheduled time): %v\n", r.Counts.Lag)
	fmt.Fprintf(w, "ack latency from scheduled time: %v\n", sends.Ack)
}

func byCode(failed map[codes.Code]int) string {
	if len(failed) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(failed))
	for c, n := range failed {
		parts = append(parts, fmt.Sprintf("%v=%d", c, n))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}
