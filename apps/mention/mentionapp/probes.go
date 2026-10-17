package mentionapp

import "github.com/ivannguyendev/chatim/apps/mention/internal/notify"

type Probe struct {
	Name   string
	Help   string
	Labels map[string]string
	Gauge  bool
	Read   func() float64
}

func (a *App) Probes() []Probe {
	c, stats, sets, watch := a.consumer, a.consumer.Stats(), a.sets, a.watch
	out := []Probe{
		{Name: "lag_seconds", Help: "Age of the oldest event in the mention consumer's last batch; 0 when idle.", Gauge: true, Read: func() float64 { return c.Lag().Seconds() }},
		{Name: "failures_total", Help: "Mention events sent back on the retry ladder after a failure.", Read: count(c.Failures)},
		{Name: "dropped_total", Help: "Mention events given up on: undecodable, invalid or gone.", Read: count(c.Dropped)},
		{Name: "stuck_total", Help: "Mention event deliveries seen for the sixth time or more.", Read: count(c.Stuck)},
		{Name: "notified_total", Help: "Mention notifications the notify stream stored.", Read: count(stats.Notified)},
		{Name: "retracted_total", Help: "Mention retractions the notify stream stored.", Read: count(stats.Retracted)},
		{Name: "notify_failures_total", Help: "Mention notifications the notify stream refused or did not ack.", Read: count(stats.Failures)},
		{Name: "room_groups_hits_total", Help: "Room group sets served from this pod's RAM.", Read: count(sets.Hits)},
		{Name: "room_groups_misses_total", Help: "Room group sets built or kicked off because the cache had none or a stale one.", Read: count(sets.Misses)},
		{Name: "room_groups_rebuild_failures_total", Help: "Background rebuilds of a large room's group set that failed.", Read: count(sets.Failures)},
		{Name: "room_groups_watch_forgets_total", Help: "Room group sets forgotten after a member_added or member_removed event.", Read: count(watch.Forgets)},
		{Name: "room_groups_watch_malformed_total", Help: "Member events skipped because their subject carried no valid room id.", Read: count(watch.Malformed)},
	}
	for _, reason := range []string{notify.ReasonStale, notify.ReasonSuperseded} {
		out = append(out, Probe{
			Name: "notify_skipped_total", Help: "Mention notifications not sent because the event was too old or superseded.",
			Labels: map[string]string{"reason": reason}, Read: func() float64 { return float64(stats.Skipped(reason)) },
		})
	}
	return out
}

func count(read func() uint64) func() float64 {
	return func() float64 { return float64(read()) }
}
