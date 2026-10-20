package metrics

import (
	"cmp"
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
		if err := reg.Register(collector(subsystem, s)); err != nil {
			return nil, fmt.Errorf("register metric %s: %w", s.Name, err)
		}
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), nil
}

func collector(defaultSubsystem string, s Source) prometheus.Collector {
	subsystem := cmp.Or(s.Subsystem, defaultSubsystem)
	if s.Gauge {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read)
	}
	return prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read)
}
