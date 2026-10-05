package main

import (
	"context"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const oplogProbeTimeout = 2 * time.Second

type probes struct {
	drops        func() publish.Drops
	router       func() actor.Stats
	cidDegraded  func() bool
	markDegraded func() bool
	cidDropped   func() uint64
	loadShed     func() int64
	oplogWindow  func() float64
	reconcile    func() reconcile.Stats
}

func metricSources(p probes) []metrics.Source {
	const dropHelp = "Events dropped before JetStream acked them; the reconciler republishes them."
	const markHelp = "Ack marks not written; the reconciler republishes those events."
	const degradedHelp = "1 while this redis client is degraded."
	out := []metrics.Source{
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return float64(p.drops().QueueFull) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "malformed"}, Read: func() float64 { return float64(p.drops().Malformed) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return float64(p.drops().Refused) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "async_failed"}, Read: func() float64 { return float64(p.drops().AsyncFailed) }},
		{Name: "ack_marks_dropped_total", Help: markHelp, Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return float64(p.drops().MarkQueueFull) }},
		{Name: "ack_marks_dropped_total", Help: markHelp, Labels: map[string]string{"reason": "marker_failed"}, Read: func() float64 { return float64(p.drops().MarkFailed) }},
		{Name: "redis_degraded", Help: degradedHelp, Gauge: true, Labels: map[string]string{"client": "cid_dedupe"}, Read: func() float64 { return flag(p.cidDegraded()) }},
		{Name: "redis_degraded", Help: degradedHelp, Gauge: true, Labels: map[string]string{"client": "ack_marks"}, Read: func() float64 { return flag(p.markDegraded()) }},
		{Name: "cid_settle_dropped_total", Help: "Cid commits or aborts dropped by the batcher.", Read: func() float64 { return float64(p.cidDropped()) }},
		{Name: "cid_pending_elsewhere_total", Help: "Sends refused because their cid is pending on another core (CD3 window).", Read: func() float64 { return float64(p.router().CIDElsewhere) }},
		{Name: "room_yields_total", Help: "Rooms an actor gave up after exhausting seq contention retries.", Read: func() float64 { return float64(p.router().Yields) }},
		{Name: "grpc_load_shed_total", Help: "gRPC calls rejected by the in-flight limiter.", Read: func() float64 { return float64(p.loadShed()) }},
		{Name: "mongo_oplog_window_seconds", Help: "Time span covered by the MongoDB oplog; NaN when unreadable.", Gauge: true, Read: p.oplogWindow},
	}
	if p.reconcile != nil {
		out = append(out, reconcileSources(p.reconcile)...)
	}
	return out
}

func reconcileSources(stats func() reconcile.Stats) []metrics.Source {
	return []metrics.Source{
		{Name: "reconcile_running", Help: "1 while this core runs a reconcile term.", Gauge: true, Read: func() float64 { return flag(stats().Running) }},
		{Name: "reconcile_lag_seconds", Help: "How far the reconciler runs behind its configured delay.", Gauge: true, Read: func() float64 { return stats().Lag.Seconds() }},
		{Name: "reconcile_terms_total", Help: "Reconcile terms started on this core.", Read: func() float64 { return float64(stats().Terms) }},
		{Name: "reconcile_republished_total", Help: "Events the reconciler sent again.", Read: func() float64 { return float64(stats().Republished) }},
		{Name: "reconcile_dropped_total", Help: "Changes that could not become events.", Read: func() float64 { return float64(stats().Dropped) }},
		{Name: "reconcile_history_lost_total", Help: "Times the change feed position fell out of the oplog.", Read: func() float64 { return float64(stats().HistoryLost) }},
	}
}

func oplogWindowSeconds(client *mongo.Client) func() float64 {
	return func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), oplogProbeTimeout)
		defer cancel()
		window, err := mongostore.OplogWindow(ctx, client)
		if err != nil {
			return math.NaN()
		}
		return window.Seconds()
	}
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
