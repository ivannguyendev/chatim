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
	dropLabels = [dropReasonCount]string{"unknown_kind", "malformed"}
	loadBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

func gaugeOfInt64(v *atomic.Int64) func() float64 { return func() float64 { return float64(v.Load()) } }

func counterOf(v *atomic.Uint64) func() float64 { return func() float64 { return float64(v.Load()) } }

type stats struct {
	users        atomic.Int64
	pairs        atomic.Int64
	loadFailures atomic.Uint64
	openRejected atomic.Uint64
	reloads      atomic.Uint64
	resyncs      [len(resyncLabels)]atomic.Uint64
	delivered    [levelCount]atomic.Uint64
	dropped      [dropReasonCount]atomic.Uint64
	loadBuckets  []atomic.Uint64
	loadCount    atomic.Uint64
	loadNanos    atomic.Int64
}

func (s *stats) init() { s.loadBuckets = make([]atomic.Uint64, len(loadBounds)) }

func (s *stats) countResync(reason chatimv1.ResyncReason) {
	if int(reason) > 0 && int(reason) < len(resyncLabels) {
		s.resyncs[reason].Add(1)
	}
}

func (s *stats) observeLoad(elapsed time.Duration) {
	elapsed = max(elapsed, 0)
	if i := sort.SearchFloat64s(loadBounds, elapsed.Seconds()); i < len(loadBounds) {
		s.loadBuckets[i].Add(1)
	}
	s.loadNanos.Add(int64(elapsed))
	s.loadCount.Add(1)
}

func (s *stats) loadHistogram() metrics.Histogram {
	h := metrics.Histogram{Bounds: loadBounds, Cumulative: make([]uint64, len(loadBounds))}
	var running uint64
	for i := range loadBounds {
		running += s.loadBuckets[i].Load()
		h.Cumulative[i] = running
	}
	h.Count = max(s.loadCount.Load(), running)
	h.Sum = time.Duration(s.loadNanos.Load()).Seconds()
	return h
}

func (t *Table) Sources() []metrics.Source {
	s := &t.stats
	out := []metrics.Source{
		{Name: "interest_users", Help: "Users with a connection on this node.", Gauge: true, Read: gaugeOfInt64(&s.users)},
		{Name: "interest_room_pairs", Help: "Loaded (user, room) memberships on this node.", Gauge: true, Read: gaugeOfInt64(&s.pairs)},
		{Name: "room_ids_load_seconds", Help: "ListMyRoomIDs latency of room loads, in seconds.", Histogram: s.loadHistogram},
		{Name: "room_ids_load_failures_total", Help: "Room loads that failed and closed the connections of the user.", Read: counterOf(&s.loadFailures)},
		{Name: "open_room_rejected_total", Help: "open_room frames answered with room_closed not_member.", Read: counterOf(&s.openRejected)},
		{Name: "interest_reloads_total", Help: "Room lists of connected users loaded again after the feed skipped part of the stream.", Read: counterOf(&s.reloads)},
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
