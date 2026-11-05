package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type Histogram struct {
	Bounds     []float64
	Cumulative []uint64
	Count      uint64
	Sum        float64
}

type histogramCollector struct {
	desc *prometheus.Desc
	read func() Histogram
}

func newHistogramCollector(fqName string, s Source) *histogramCollector {
	return &histogramCollector{desc: prometheus.NewDesc(fqName, s.Help, nil, s.Labels), read: s.Histogram}
}

func (c *histogramCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *histogramCollector) Collect(ch chan<- prometheus.Metric) {
	h := c.read()
	if len(h.Bounds) != len(h.Cumulative) {
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("histogram has %d bounds and %d bucket counts", len(h.Bounds), len(h.Cumulative)))
		return
	}
	buckets := make(map[float64]uint64, len(h.Bounds))
	for i, bound := range h.Bounds {
		buckets[bound] = h.Cumulative[i]
	}
	m, err := prometheus.NewConstHistogram(c.desc, h.Count, h.Sum, buckets)
	if err != nil {
		m = prometheus.NewInvalidMetric(c.desc, err)
	}
	ch <- m
}
