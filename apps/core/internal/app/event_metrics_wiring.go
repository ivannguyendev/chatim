package app

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type stateClearProbe interface {
	Cleared() uint64
	Dropped() uint64
	Failures() uint64
}

type readBatchProbe interface {
	Entries() uint64
	Events() uint64
	Dropped() uint64
	EnqueueFailures() uint64
}

type projectionProbe interface {
	Name() string
	Lag() time.Duration
	Failures() uint64
	Dropped() uint64
	Stuck() uint64
}

type sweepProbe interface {
	PendingOldest(c store.StateCollection) time.Duration
	Republished(c store.StateCollection) uint64
	Dropped(c store.StateCollection) uint64
	PublishFailures() uint64
	Failures() uint64
	Passes() uint64
}

type eventProbes struct {
	clears     stateClearProbe
	reads      readBatchProbe
	projection projectionProbe
	sweeper    sweepProbe
}

func eventSources(p eventProbes, drops func() publish.Drops) []metrics.Source {
	out := []metrics.Source{
		{Name: "state_clear_cleared_total", Help: "Pending event flags cleared after JetStream acked their event.", Read: count(p.clears.Cleared)},
		{Name: "state_clear_dropped_total", Help: "Pending flag clears dropped (queue full, closed or stopped); the sweeper republishes those events.", Read: count(p.clears.Dropped)},
		{Name: "state_clear_failures_total", Help: "Pending flag clear batches the store refused; the sweeper republishes those events.", Read: count(p.clears.Failures)},
		{Name: "publish_state_track_full_total", Help: "Published events whose flag clear was not followed because the ack follower queue was full.", Read: func() float64 { return float64(drops().StateTrackFull) }},
		{Name: "read_batch_entries_total", Help: "Read position moves handed to the read batch queue.", Read: count(p.reads.Entries)},
		{Name: "read_batch_events_total", Help: "read_positions events the read batch queue handed to the publisher.", Read: count(p.reads.Events)},
		{Name: "read_batch_dropped_total", Help: "Read position moves dropped after the read batch queue closed; the sweeper republishes them.", Read: count(p.reads.Dropped)},
		{Name: "read_batch_enqueue_failures_total", Help: "read_positions events the publisher refused; the sweeper republishes their read positions.", Read: count(p.reads.EnqueueFailures)},
		{Name: "sweep_publish_failures_total", Help: "Pending events the sweeper could not republish; their flag stays for the next pass.", Read: count(p.sweeper.PublishFailures)},
		{Name: "sweep_failures_total", Help: "Pending documents or flag clears the sweeper failed on; they stay flagged for the next pass.", Read: count(p.sweeper.Failures)},
		{Name: "sweep_passes_total", Help: "Sweeper passes this core finished while it held the leader slot.", Read: count(p.sweeper.Passes)},
	}
	for _, c := range store.AllStateCollections() {
		labels := map[string]string{"collection": c.String()}
		out = append(out,
			metrics.Source{Name: "event_pending_oldest_seconds", Help: "Age of the oldest pending event flag the sweeper saw in this collection.", Gauge: true, Labels: labels, Read: func() float64 { return p.sweeper.PendingOldest(c).Seconds() }},
			metrics.Source{Name: "sweep_republished_total", Help: "Pending events the sweeper republished that the stream had not stored yet.", Labels: labels, Read: func() float64 { return float64(p.sweeper.Republished(c)) }},
			metrics.Source{Name: "sweep_dropped_total", Help: "Pending flags the sweeper cleared without an event because the document is gone or cannot become an event.", Labels: labels, Read: func() float64 { return float64(p.sweeper.Dropped(c)) }},
		)
	}
	if pr := p.projection; pr != nil {
		labels := map[string]string{"projection": pr.Name()}
		out = append(out,
			metrics.Source{Name: "projection_lag_seconds", Help: "Age of the oldest event in the projection's last batch; 0 when idle.", Gauge: true, Labels: labels, Read: func() float64 { return pr.Lag().Seconds() }},
			metrics.Source{Name: "projection_failures_total", Help: "Events the projection failed and sent back on the retry ladder.", Labels: labels, Read: count(pr.Failures)},
			metrics.Source{Name: "projection_dropped_total", Help: "Events the projection gave up on: undecodable, or the room or document is gone.", Labels: labels, Read: count(pr.Dropped)},
			metrics.Source{Name: "projection_stuck_total", Help: "Deliveries the projection saw for the sixth time or more.", Labels: labels, Read: count(pr.Stuck)},
		)
	}
	return out
}

func count(read func() uint64) func() float64 {
	return func() float64 { return float64(read()) }
}
