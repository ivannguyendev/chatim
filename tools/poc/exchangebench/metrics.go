package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const cpuSeries = "process_cpu_seconds_total"

var trackedSeries = []string{"exchanges_total", "jit_writes_total", "store_errors_total", "trust_reload_errors_total", cpuSeries}

type scraper struct {
	url  string
	http *http.Client
}

type sample struct {
	at     time.Time
	values map[string]float64
}

func newScraper(endpoint string, timeout time.Duration) *scraper {
	return &scraper{url: endpoint, http: &http.Client{Timeout: timeout}}
}

func (s *scraper) scrape(ctx context.Context) (sample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return sample{}, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return sample{}, fmt.Errorf("scrape %s: %w", s.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return sample{}, fmt.Errorf("scrape %s: status %d", s.url, resp.StatusCode)
	}
	values, err := parseSeries(resp.Body)
	return sample{at: time.Now(), values: values}, err
}

func parseSeries(r io.Reader) (map[string]float64, error) {
	out := map[string]float64{}
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		line := lines.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cut := strings.LastIndexByte(line, ' ')
		if cut <= 0 || !tracked(line[:cut]) {
			continue
		}
		v, err := strconv.ParseFloat(line[cut+1:], 64)
		if err != nil {
			continue
		}
		out[line[:cut]] = v
	}
	return out, lines.Err()
}

func tracked(series string) bool {
	name, _, _ := strings.Cut(series, "{")
	for _, t := range trackedSeries {
		if strings.HasSuffix(name, t) {
			return true
		}
	}
	return false
}

func writeDeltas(w io.Writer, before, after sample) {
	span := after.at.Sub(before.at)
	fmt.Fprintf(w, "identity metrics over %v (scrape before the first shot to scrape after the last answer):\n", span.Round(time.Millisecond))
	for _, series := range slices.Sorted(maps.Keys(after.values)) {
		delta := after.values[series] - before.values[series]
		if series == cpuSeries {
			fmt.Fprintf(w, "  %s +%.2fs = %.2f cores\n", series, delta, delta/span.Seconds())
			continue
		}
		fmt.Fprintf(w, "  %s +%.0f\n", series, delta)
	}
}
