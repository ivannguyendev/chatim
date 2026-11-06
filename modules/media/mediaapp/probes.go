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
	return out
}
