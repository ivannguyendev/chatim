package app

import (
	"context"
	"maps"
	"math"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const (
	oplogProbeTimeout = 2 * time.Second
	metricsSubsystem  = "core"
)

type probes struct {
	drops           func() publish.Drops
	router          func() actor.Stats
	cidDegraded     func() bool
	cidDropped      func() uint64
	loadShed        func() int64
	oplogWindow     func() float64
	counterRepairs  map[string]func() uint64
	timerFailures   func() uint64
	timerDropped    func() uint64
	memberForgets   func() uint64
	memberMalformed func() uint64
	roomCache       roomCacheProbes
	events          eventProbes
	mention         []mentionapp.Probe
}

func metricSources(p probes) []metrics.Source {
	const dropHelp = "Events dropped before JetStream acked them; the sweeper sends them again."
	const degradedHelp = "1 while this redis client is degraded."
	out := []metrics.Source{
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return float64(p.drops().QueueFull) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "malformed"}, Read: func() float64 { return float64(p.drops().Malformed) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return float64(p.drops().Refused) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "async_failed"}, Read: func() float64 { return float64(p.drops().AsyncFailed) }},
		{Name: "redis_degraded", Help: degradedHelp, Gauge: true, Labels: map[string]string{"client": "cid_dedupe"}, Read: func() float64 { return flag(p.cidDegraded()) }},
		{Name: "cid_settle_dropped_total", Help: "Cid commits or aborts dropped by the batcher.", Read: func() float64 { return float64(p.cidDropped()) }},
		{Name: "cid_pending_elsewhere_total", Help: "Sends refused because their cid is pending on another core (CD3 window).", Read: func() float64 { return float64(p.router().CIDElsewhere) }},
		{Name: "room_yields_total", Help: "Rooms an actor gave up after exhausting seq contention retries.", Read: func() float64 { return float64(p.router().Yields) }},
		{Name: "grpc_load_shed_total", Help: "gRPC calls rejected by the in-flight limiter.", Read: func() float64 { return float64(p.loadShed()) }},
		{Name: "mongo_oplog_window_seconds", Help: "Time span covered by the MongoDB oplog; NaN when unreadable.", Gauge: true, Read: p.oplogWindow},
	}
	out = append(out, eventSources(p.events, p.drops)...)
	out = append(out, counterSources(p.counterRepairs)...)
	out = append(out, timerSources(p.timerFailures, p.timerDropped)...)
	out = append(out, memberWatchSources(p.memberForgets, p.memberMalformed)...)
	out = append(out, roomCacheSources(p.roomCache)...)
	return append(out, mentionSources(p.mention)...)
}

func counterSources(repairs map[string]func() uint64) []metrics.Source {
	const help = "Counter summaries the survival timer repairs rewrote because the fast path left them stale."
	out := make([]metrics.Source, 0, len(repairs))
	for _, name := range slices.Sorted(maps.Keys(repairs)) {
		read := repairs[name]
		out = append(out, metrics.Source{Name: "counter_repaired_total", Help: help, Labels: map[string]string{"counter": name}, Read: func() float64 { return float64(read()) }})
	}
	return out
}

func timerSources(failures, dropped func() uint64) []metrics.Source {
	return []metrics.Source{
		{Name: "timer_failures_total", Help: "Fired count check timers sent back for a later retry after their repair failed.", Read: func() float64 { return float64(failures()) }},
		{Name: "timer_dropped_total", Help: "Fired count check timers given up on: the room or message is gone or the payload is malformed.", Read: func() float64 { return float64(dropped()) }},
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
