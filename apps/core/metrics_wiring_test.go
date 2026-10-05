package main

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
)

var readerMetrics = []string{
	"reconcile_running", "reconcile_terms_total", "reconcile_forwarded_total", "reconcile_dropped_total", "reconcile_history_lost_total",
}

func fakeProbes(withReader bool) probes {
	zero := func() uint64 { return 0 }
	p := probes{
		drops:        func() publish.Drops { return publish.Drops{} },
		router:       func() actor.Stats { return actor.Stats{} },
		cidDegraded:  func() bool { return false },
		markDegraded: func() bool { return false },
		cidDropped:   zero,
		loadShed:     func() int64 { return 0 },
		oplogWindow:  func() float64 { return 0 },
		workers:      func() effects.Stats { return effects.Stats{Processed: 7, Failed: 2, Lag: 3 * time.Second} },
		effectCounts: map[string]effectCounters{
			"msg_created":  {republished: func() uint64 { return 5 }, dropped: zero},
			"room_created": {republished: zero, dropped: func() uint64 { return 1 }},
		},
	}
	if withReader {
		p.reconcile = func() reconcile.Stats { return reconcile.Stats{} }
	}
	return p
}

func sourceNames(sources []metrics.Source) map[string]bool {
	out := map[string]bool{}
	for _, s := range sources {
		out[s.Name] = true
	}
	return out
}

func TestCoreMetricSourcesCoverEveryGuarantee(t *testing.T) {
	everyCore := []string{
		"publish_dropped_total", "ack_marks_dropped_total", "redis_degraded", "cid_settle_dropped_total",
		"cid_pending_elsewhere_total", "room_yields_total", "grpc_load_shed_total", "mongo_oplog_window_seconds",
		"reconcile_lag_seconds", "reconcile_republished_total", "effect_dropped_total", "work_processed_total", "work_failures_total",
	}
	on := sourceNames(metricSources(fakeProbes(true)))
	for _, name := range append(everyCore, readerMetrics...) {
		if !on[name] {
			t.Errorf("no metric source %s", name)
		}
	}
	if _, err := metrics.Handler(metricSources(fakeProbes(true))); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	off := sourceNames(metricSources(fakeProbes(false)))
	for _, name := range readerMetrics {
		if off[name] {
			t.Errorf("reader disabled but %s is exported", name)
		}
	}
	for _, name := range everyCore {
		if !off[name] {
			t.Errorf("%s must be exported on every core, also without the reader", name)
		}
	}
}

func TestEffectMetricsReadTheirEffectByLabel(t *testing.T) {
	got := map[string]float64{}
	for _, s := range metricSources(fakeProbes(false)) {
		key := s.Name
		if effect := s.Labels["effect"]; effect != "" {
			key += "{" + effect + "}"
		}
		got[key] = s.Read()
	}
	want := map[string]float64{
		"reconcile_lag_seconds": 3, "work_processed_total": 7, "work_failures_total": 2,
		"reconcile_republished_total{msg_created}": 5, "reconcile_republished_total{room_created}": 0,
		"effect_dropped_total{msg_created}": 0, "effect_dropped_total{room_created}": 1,
	}
	for key, v := range want {
		if g, ok := got[key]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", key, g, ok, v)
		}
	}
}
