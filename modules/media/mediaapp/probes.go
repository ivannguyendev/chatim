package mediaapp

type Probe struct {
	Name   string
	Help   string
	Labels map[string]string
	Gauge  bool
	Read   func() float64
}

func (a *App) Probes() []Probe {
	out := []Probe{{Name: "ready", Help: "1 when media is configured and both buckets answer; 0 otherwise.", Gauge: true, Read: func() float64 {
		if a.Ready() {
			return 1
		}
		return 0
	}}}
	if a.uploads != nil {
		up := a.uploads
		out = append(out,
			Probe{Name: "uploads_started_total", Help: "Upload sessions opened.", Read: func() float64 { return float64(up.Started()) }},
			Probe{Name: "uploads_completed_total", Help: "Upload sessions this node moved to uploaded.", Read: func() float64 { return float64(up.Completed()) }},
		)
	}
	if a.objects != nil {
		for _, c := range a.objects.Stats().Counts() {
			out = append(out, Probe{
				Name: "storage_operations_total", Help: "S3 calls by operation and result (ok, client mistake, storage error).",
				Labels: map[string]string{"operation": c.Operation, "result": c.Result}, Read: func() float64 { return float64(c.Read()) },
			})
		}
	}
	return append(out, a.consumerProbes()...)
}

func (a *App) consumerProbes() []Probe {
	if a.consumer == nil {
		return nil
	}
	c, stats := a.consumer, a.attach.Stats()
	out := []Probe{
		{Name: "lag_seconds", Help: "Age of the oldest event in the media consumer's last batch; 0 when idle.", Gauge: true, Read: func() float64 { return c.Lag().Seconds() }},
		{Name: "stuck_total", Help: "Media event deliveries that failed for the sixth time or more, not counting waits for an upload.", Read: func() float64 { return float64(c.Stuck()) }},
	}
	for _, d := range stats.DroppedCounts() {
		out = append(out, Probe{
			Name: "dropped_total", Help: "Media events or message files given up on, by reason.",
			Labels: map[string]string{"reason": d.Label}, Read: func() float64 { return float64(d.Read()) },
		})
	}
	for _, m := range stats.MoveCounts() {
		out = append(out, Probe{
			Name: "moves_total", Help: "Moves of a file to the completed bucket, by result; cleanup_error is a processing delete left to the retention rule.",
			Labels: map[string]string{"result": m.Label}, Read: func() float64 { return float64(m.Read()) },
		})
	}
	return out
}
