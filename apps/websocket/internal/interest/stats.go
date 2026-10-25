package interest

import (
	"sort"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type dropReason int

const (
	dropUnknownKind dropReason = iota
	dropMalformed
	dropReasonCount
)

var (
	levelLabels  = [levelCount]string{"member", "open", "owner"}
	resyncLabels = [...]string{
		chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD:    "lag_too_old",
		chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST:  "consumer_lost",
		chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG: "client_backlog",
	}
	dropLabels     = [dropReasonCount]string{"unknown_kind", "malformed"}
	loadBounds     = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	reloadBounds   = []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}
	deliveryBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
)

func gaugeOfInt64(v *atomic.Int64) func() float64 { return func() float64 { return float64(v.Load()) } }

func counterOf(v *atomic.Uint64) func() float64 { return func() float64 { return float64(v.Load()) } }

type histogram struct {
	bounds  []float64
	buckets []atomic.Uint64
	count   atomic.Uint64
	nanos   atomic.Int64
}

func newHistogram(bounds []float64) *histogram {
	return &histogram{bounds: bounds, buckets: make([]atomic.Uint64, len(bounds))}
}

func (h *histogram) observe(elapsed time.Duration) {
	elapsed = max(elapsed, 0)
	if i := sort.SearchFloat64s(h.bounds, elapsed.Seconds()); i < len(h.bounds) {
		h.buckets[i].Add(1)
	}
	h.nanos.Add(int64(elapsed))
	h.count.Add(1)
}

func (h *histogram) read() metrics.Histogram {
	out := metrics.Histogram{Bounds: h.bounds, Cumulative: make([]uint64, len(h.bounds))}
	var running uint64
	for i := range h.bounds {
		running += h.buckets[i].Load()
		out.Cumulative[i] = running
	}
	out.Count = max(h.count.Load(), running)
	out.Sum = time.Duration(h.nanos.Load()).Seconds()
	return out
}

type stats struct {
	users          atomic.Int64
	pairs          atomic.Int64
	suspended      atomic.Int64
	loadFailures   atomic.Uint64
	openRejected   atomic.Uint64
	reloads        atomic.Uint64
	reloadFailures atomic.Uint64
	resyncs        [len(resyncLabels)]atomic.Uint64
	delivered      [levelCount]atomic.Uint64
	dropped        [dropReasonCount]atomic.Uint64
	load           *histogram
	reload         *histogram
	delivery       *histogram
}

func (s *stats) init() {
	s.load = newHistogram(loadBounds)
	s.reload = newHistogram(reloadBounds)
	s.delivery = newHistogram(deliveryBounds)
}

func (s *stats) countResync(reason chatimv1.ResyncReason) {
	if int(reason) > 0 && int(reason) < len(resyncLabels) {
		s.resyncs[reason].Add(1)
	}
}

func (t *Table) Sources() []metrics.Source {
	s := &t.stats
	out := []metrics.Source{
		{Name: "interest_users", Help: "Users with a connection on this node.", Gauge: true, Read: gaugeOfInt64(&s.users)},
		{Name: "interest_room_pairs", Help: "Loaded (user, room) memberships on this node.", Gauge: true, Read: gaugeOfInt64(&s.pairs)},
		{Name: "room_ids_load_seconds", Help: "ListMyRoomIDs latency of room loads, in seconds.", Histogram: s.load.read},
		{Name: "room_ids_load_failures_total", Help: "First room loads at login that failed; the connections of the user got load_failed.", Read: counterOf(&s.loadFailures)},
		{Name: "open_room_rejected_total", Help: "open_room frames answered with room_closed not_member.", Read: counterOf(&s.openRejected)},
		{Name: "users_suspended", Help: "Users whose delivery is suspended until their rooms are loaded again after the feed skipped part of the stream.", Gauge: true, Read: gaugeOfInt64(&s.suspended)},
		{Name: "reloads_total", Help: "Suspended users whose rooms were loaded again.", Read: counterOf(&s.reloads)},
		{Name: "reload_seconds", Help: "Time from the first suspension of a user to the end of its reload, in seconds.", Histogram: s.reload.read},
		{Name: "event_delivery_seconds", Help: "Time from the JetStream store time of a core event to its frame queued to the first connections on this node, in seconds.", Histogram: s.delivery.read},
		{Name: "reload_failures_total", Help: "Reloads of suspended users that failed; the connections of the user got load_failed.", Read: counterOf(&s.reloadFailures)},
	}
	for reason, label := range resyncLabels {
		if label == "" {
			continue
		}
		out = append(out, metrics.Source{
			Name: "resyncs_total", Help: "resync frames queued to connections, by reason.",
			Labels: map[string]string{"reason": label}, Read: counterOf(&s.resyncs[reason]),
		})
	}
	for l := range levelCount {
		out = append(out, metrics.Source{
			Name: "events_delivered_total", Help: "Event frames queued to connections, by interest level.",
			Labels: map[string]string{"level": levelLabels[l]}, Read: counterOf(&s.delivered[l]),
		})
	}
	for r := range dropReasonCount {
		out = append(out, metrics.Source{
			Name: "events_dropped_total", Help: "Core events dropped before routing, by reason.",
			Labels: map[string]string{"reason": dropLabels[r]}, Read: counterOf(&s.dropped[r]),
		})
	}
	return out
}
