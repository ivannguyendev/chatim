package main

import (
	"context"
	"maps"
	"math"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const oplogProbeTimeout = 2 * time.Second

type effectCounters struct {
	republished func() uint64
	dropped     func() uint64
}

type probes struct {
	drops        func() publish.Drops
	router       func() actor.Stats
	cidDegraded  func() bool
	markDegraded func() bool
	cidDropped   func() uint64
	loadShed     func() int64
	oplogWindow  func() float64
	workers      func() effects.Stats
	effectCounts map[string]effectCounters
	reconcile    func() reconcile.Stats
}

func metricSources(p probes) []metrics.Source {
	const dropHelp = "Events dropped before JetStream acked them; the effect workers send them again."
	const markHelp = "Ack marks not written; the effect workers send those events again."
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
	out = append(out, workerSources(p.workers, p.effectCounts)...)
	if p.reconcile != nil {
		out = append(out, readerSources(p.reconcile)...)
	}
	return out
}

func workerSources(stats func() effects.Stats, counts map[string]effectCounters) []metrics.Source {
	const republishHelp = "Events effect workers sent and JetStream acked; msg_created sends only unmarked events, msg_changed counts only ids the stream had not stored."
	const dropHelp = "Work records an effect gave up on (missing room, message or edit fact, or a corrupt document)."
	out := []metrics.Source{
		{Name: "reconcile_lag_seconds", Help: "How far the effect workers run behind each effect's delay.", Gauge: true, Read: func() float64 { return stats().Lag.Seconds() }},
		{Name: "work_processed_total", Help: "Work records acked after every effect ran.", Read: func() float64 { return float64(stats().Processed) }},
		{Name: "work_failures_total", Help: "Work records sent back for retry after an effect failed.", Read: func() float64 { return float64(stats().Failed) }},
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		c, labels := counts[name], map[string]string{"effect": name}
		if c.republished != nil {
			out = append(out, metrics.Source{Name: "reconcile_republished_total", Help: republishHelp, Labels: labels, Read: func() float64 { return float64(c.republished()) }})
		}
		out = append(out, metrics.Source{Name: "effect_dropped_total", Help: dropHelp, Labels: labels, Read: func() float64 { return float64(c.dropped()) }})
	}
	return out
}

func readerSources(stats func() reconcile.Stats) []metrics.Source {
	return []metrics.Source{
		{Name: "reconcile_running", Help: "1 while this core runs the change reader.", Gauge: true, Read: func() float64 { return flag(stats().Running) }},
		{Name: "reconcile_terms_total", Help: "Reader terms started on this core.", Read: func() float64 { return float64(stats().Terms) }},
		{Name: "reconcile_forwarded_total", Help: "Committed changes the reader sent to the work stream.", Read: func() float64 { return float64(stats().Forwarded) }},
		{Name: "reconcile_dropped_total", Help: "Corrupt changes the reader could not turn into work records.", Read: func() float64 { return float64(stats().Dropped) }},
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
