package main

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
)

func fakeProbes(withReconciler bool) probes {
	p := probes{
		drops:        func() publish.Drops { return publish.Drops{} },
		router:       func() actor.Stats { return actor.Stats{} },
		cidDegraded:  func() bool { return false },
		markDegraded: func() bool { return false },
		cidDropped:   func() uint64 { return 0 },
		loadShed:     func() int64 { return 0 },
		oplogWindow:  func() float64 { return 0 },
	}
	if withReconciler {
		p.reconcile = func() reconcile.Stats { return reconcile.Stats{} }
	}
	return p
}

func TestCoreMetricSourcesCoverEveryGuarantee(t *testing.T) {
	want := []string{
		"publish_dropped_total", "ack_marks_dropped_total", "redis_degraded", "cid_settle_dropped_total",
		"cid_pending_elsewhere_total", "room_yields_total", "grpc_load_shed_total", "mongo_oplog_window_seconds",
		"reconcile_running", "reconcile_terms_total", "reconcile_forwarded_total",
		"reconcile_dropped_total", "reconcile_history_lost_total",
	}
	got := map[string]bool{}
	for _, s := range metricSources(fakeProbes(true)) {
		got[s.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("no metric source %s", name)
		}
	}
	if _, err := metrics.Handler(metricSources(fakeProbes(true))); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, s := range metricSources(fakeProbes(false)) {
		if strings.HasPrefix(s.Name, "reconcile_") {
			t.Errorf("reconciler disabled but %s is exported", s.Name)
		}
	}
}
