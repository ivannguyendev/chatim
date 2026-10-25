package metrics

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "chatim"

type Source struct {
	Subsystem string
	Name      string
	Help      string
	Labels    map[string]string
	Gauge     bool
	Read      func() float64
	Histogram func() Histogram
}

func Handler(subsystem string, sources []Source) (http.Handler, error) {
	reg := prometheus.NewRegistry()
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	for _, s := range sources {
		c, err := collector(subsystem, s)
		if err == nil {
			err = reg.Register(c)
		}
		if err != nil {
			return nil, fmt.Errorf("register metric %s: %w", s.Name, err)
		}
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), nil
}

func collector(defaultSubsystem string, s Source) (prometheus.Collector, error) {
	subsystem := cmp.Or(s.Subsystem, defaultSubsystem)
	switch {
	case (s.Read == nil) == (s.Histogram == nil):
		return nil, errors.New("needs exactly one of Read and Histogram")
	case s.Histogram != nil && s.Gauge:
		return nil, errors.New("a histogram cannot be a gauge")
	case s.Histogram != nil:
		return newHistogramCollector(prometheus.BuildFQName(namespace, subsystem, s.Name), s), nil
	case s.Gauge:
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read), nil
	default:
		return prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read), nil
	}
}
