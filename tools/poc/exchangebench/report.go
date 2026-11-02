package main

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

type stats struct {
	mu    sync.Mutex
	codes map[int]int
	ok    latency.Recorder
	all   latency.Recorder
}

func (s *stats) record(code int, took time.Duration) {
	s.all.Add(took)
	if code == http.StatusOK {
		s.ok.Add(took)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codes == nil {
		s.codes = map[int]int{}
	}
	s.codes[code]++
}

func (s *stats) snapshot() map[int]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.codes)
}

func (s *stats) write(w io.Writer, p openloop.Plan, c openloop.Counts) {
	codes := s.snapshot()
	secs := p.Duration.Seconds()
	m := c.Measured
	fmt.Fprintf(w, "rate target=%d/s sent=%.0f/s ok=%.0f/s over the %v measured window (after %v warmup), pacer elapsed %v\n",
		p.Rate, float64(m.Issued)/secs, float64(codes[http.StatusOK])/secs, p.Duration, p.Warmup, c.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(w, "exchanges due=%d sent=%d shed_by_client=%d (max in-flight %d); with warmup due=%d sent=%d shed=%d\n",
		m.Due, m.Issued, m.Shed, p.MaxInflight, c.All.Due, c.All.Issued, c.All.Shed)
	fmt.Fprintf(w, "http codes: %s; 5xx=%d\n", byCode(codes), serverErrors(codes))
	fmt.Fprintf(w, "pacer lag (issue minus scheduled time): %v\n", c.Lag)
	fmt.Fprintf(w, "latency of 200 from scheduled time: %v\n", s.ok.Summary())
	fmt.Fprintf(w, "latency of every answer from scheduled time: %v\n", s.all.Summary())
}

func byCode(codes map[int]int) string {
	if len(codes) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(codes))
	for _, code := range slices.Sorted(maps.Keys(codes)) {
		name := strconv.Itoa(code)
		if code == transportError {
			name = "transport"
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, codes[code]))
	}
	return strings.Join(parts, " ")
}

func serverErrors(codes map[int]int) int {
	n := 0
	for code, count := range codes {
		if code >= http.StatusInternalServerError {
			n += count
		}
	}
	return n
}
