package main

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

type cacheCounts map[string]float64

func scrapeCaches(ctx context.Context, url string) (cacheCounts, error) {
	out := cacheCounts{}
	if url == "" {
		return out, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		for _, kind := range []string{"hits", "misses"} {
			prefix := "chatim_core_room_cache_" + kind + "_total{cache=\""
			rest, ok := strings.CutPrefix(line, prefix)
			if !ok {
				continue
			}
			name, value, ok := strings.Cut(rest, "\"} ")
			if !ok {
				continue
			}
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fmt.Errorf("metric %q: %w", line, err)
			}
			out[name+" "+kind] = v
		}
	}
	return out, sc.Err()
}

func (c cacheCounts) minus(b cacheCounts) cacheCounts {
	out := cacheCounts{}
	for k, v := range c {
		if d := v - b[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func (c cacheCounts) add(d cacheCounts) {
	for k, v := range d {
		c[k] += v
	}
}

func (c cacheCounts) per(n int) cacheCounts {
	out := cacheCounts{}
	for k, v := range c {
		out[k] = v / float64(max(n, 1))
	}
	return out
}

func (c cacheCounts) String() string {
	if len(c) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(c))
	for _, k := range slices.Sorted(maps.Keys(c)) {
		parts = append(parts, fmt.Sprintf("%s=%.1f", strings.ReplaceAll(k, " ", "_"), c[k]))
	}
	return strings.Join(parts, " ")
}
