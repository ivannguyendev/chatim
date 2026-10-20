package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type fakeClears struct{}

func (fakeClears) Cleared() uint64  { return 31 }
func (fakeClears) Dropped() uint64  { return 32 }
func (fakeClears) Failures() uint64 { return 33 }

type fakeProjection struct {
	name string
	base uint64
}

func (p fakeProjection) Name() string       { return p.name }
func (p fakeProjection) Lag() time.Duration { return time.Duration(p.base) * time.Second }
func (p fakeProjection) Failures() uint64   { return p.base + 1 }
func (p fakeProjection) Dropped() uint64    { return p.base + 2 }
func (p fakeProjection) Stuck() uint64      { return p.base + 3 }

type fakeSweeper struct{}

func (fakeSweeper) PendingOldest(c store.StateCollection) time.Duration {
	return time.Duration(c) * time.Second
}
func (fakeSweeper) Republished(c store.StateCollection) uint64 { return 10 + uint64(c) }
func (fakeSweeper) Dropped(c store.StateCollection) uint64     { return 20 + uint64(c) }
func (fakeSweeper) PublishFailures() uint64                    { return 41 }
func (fakeSweeper) Failures() uint64                           { return 42 }
func (fakeSweeper) Passes() uint64                             { return 43 }

func fakeProbes() probes {
	zero := func() uint64 { return 0 }
	return probes{
		drops:       func() publish.Drops { return publish.Drops{StateTrackFull: 34} },
		router:      func() actor.Stats { return actor.Stats{} },
		cidDegraded: func() bool { return false },
		cidDropped:  zero,
		loadShed:    func() int64 { return 0 },
		oplogWindow: func() float64 { return 0 },
		counterRepairs: map[string]func() uint64{
			"reactions": func() uint64 { return 6 }, "replies": func() uint64 { return 21 }, "members": func() uint64 { return 12 },
		},
		timerFailures:   func() uint64 { return 17 },
		timerDropped:    func() uint64 { return 18 },
		memberForgets:   func() uint64 { return 13 },
		memberMalformed: func() uint64 { return 14 },
		roomCache:       fakeRoomCacheProbes(),
		events: eventProbes{
			clears:     fakeClears{},
			projection: fakeProjection{"projections", 50},
			sweeper:    fakeSweeper{},
		},
	}
}

func readings(sources []metrics.Source) map[string]float64 {
	got := map[string]float64{}
	for _, s := range sources {
		var key strings.Builder
		key.WriteString(s.Name)
		for _, label := range []string{"counter", "projection", "collection", "client", "reason"} {
			if v := s.Labels[label]; v != "" {
				key.WriteString("{" + v + "}")
			}
		}
		got[key.String()] = s.Read()
	}
	return got
}

func TestCoreMetricSourcesCoverEveryGuarantee(t *testing.T) {
	sources := metricSources(fakeProbes())
	if _, err := metrics.Handler(sources); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	got := readings(sources)
	for _, name := range []string{
		"publish_dropped_total{queue_full}", "redis_degraded{cid_dedupe}", "cid_settle_dropped_total",
		"cid_pending_elsewhere_total", "room_yields_total", "grpc_load_shed_total", "mongo_oplog_window_seconds",
		"counter_repaired_total{members}", "timer_failures_total", "timer_dropped_total", "member_cache_forgets_total", "member_watch_malformed_total",
		"room_tail_cache_bytes", "room_watch_forgets_total", "room_watch_malformed_total",
		"state_clear_cleared_total", "state_clear_dropped_total", "state_clear_failures_total", "publish_state_track_full_total",
		"sweep_publish_failures_total", "sweep_failures_total", "sweep_passes_total",
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("no metric source %s", name)
		}
	}
	for _, gone := range []string{
		"ack_marks_dropped_total{queue_full}", "redis_degraded{ack_marks}", "reconcile_lag_seconds", "work_processed_total", "work_failures_total",
	} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s is still exported without the effect engine", gone)
		}
	}
}

func TestEventMetricsReadTheirComponentByLabel(t *testing.T) {
	got := readings(metricSources(fakeProbes()))
	want := map[string]float64{
		"state_clear_cleared_total": 31, "state_clear_dropped_total": 32, "state_clear_failures_total": 33, "publish_state_track_full_total": 34,
		"sweep_publish_failures_total": 41, "sweep_failures_total": 42, "sweep_passes_total": 43,
		"projection_lag_seconds{projections}": 50, "projection_failures_total{projections}": 51,
		"projection_dropped_total{projections}": 52, "projection_stuck_total{projections}": 53,
		"counter_repaired_total{reactions}": 6, "counter_repaired_total{replies}": 21, "counter_repaired_total{members}": 12,
		"timer_failures_total": 17, "timer_dropped_total": 18,
	}
	for _, c := range store.AllStateCollections() {
		want["event_pending_oldest_seconds{"+c.String()+"}"] = float64(c)
		want["sweep_republished_total{"+c.String()+"}"] = float64(10 + c)
		want["sweep_dropped_total{"+c.String()+"}"] = float64(20 + c)
	}
	for key, v := range want {
		if g, ok := got[key]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", key, g, ok, v)
		}
	}
}
