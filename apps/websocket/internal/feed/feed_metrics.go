package feed

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

func (f *Feed) consumeErrors(stream string) jetstream.ConsumeErrHandler {
	limited := jsconsume.NewLimitedLog(f.log)
	return func(_ jetstream.ConsumeContext, err error) {
		limited.Warn(context.Background(), "nats consume error; the ordered consumer recovers on its own", "stream", stream, "err", err)
	}
}

func (f *Feed) reconnects() float64 {
	b := f.broker.Load()
	if b == nil {
		return 0
	}
	return float64((*b).Reconnects())
}

func (f *Feed) queueDepth() float64 {
	if fan := f.fan.Load(); fan != nil {
		return float64(fan.depth())
	}
	return 0
}

func (f *Feed) Sources() []metrics.Source {
	out := []metrics.Source{
		{Name: "nats_connected", Help: "1 while the NATS connection is up, else 0.", Gauge: true, Read: func() float64 { return boolGauge(f.Connected()) }},
		{Name: "nats_reconnects_total", Help: "NATS reconnects since start.", Read: f.reconnects},
		{Name: "fanout_queue_depth", Help: "Events queued for the fanout workers.", Gauge: true, Read: f.queueDepth},
		{Name: "events_unknown_total", Help: "Stream messages dropped for a malformed subject or missing metadata.", Read: func() float64 { return float64(f.unknown.Load()) }},
	}
	for _, s := range f.streams {
		labels := map[string]string{"stream": s.stream}
		out = append(out,
			metrics.Source{Name: "consumer_lag_seconds", Help: "Age of the last delivered message by its stream store time, measured at delivery.", Labels: labels, Gauge: true, Read: s.lagSeconds},
			metrics.Source{Name: "consumer_pending", Help: "Messages left in the stream after the last delivered one.", Labels: labels, Gauge: true, Read: func() float64 { return float64(s.pending.Load()) }},
			metrics.Source{Name: "consumer_resets_total", Help: "Times the ordered consumer was recreated by the client library.", Labels: labels, Read: func() float64 { return float64(s.resets.Load()) }},
		)
	}
	return out
}

func boolGauge(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
