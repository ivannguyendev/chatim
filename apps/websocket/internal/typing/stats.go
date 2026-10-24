package typing

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

type dropReason int

const (
	dropRate dropReason = iota
	dropNotMember
	dropMalformed
	dropQueueFull
	dropSendFull
	dropReasonCount
)

var dropLabels = [dropReasonCount]string{"rate", "not_member", "malformed", "queue_full", "send_full"}

type stats struct {
	received      atomic.Uint64
	delivered     atomic.Uint64
	relayFailures atomic.Uint64
	dropped       [dropReasonCount]atomic.Uint64
}

func (s *stats) drop(reason dropReason) { s.dropped[reason].Add(1) }

func (s *stats) dropN(reason dropReason, n int) { addCount(&s.dropped[reason], n) }

func addCount(v *atomic.Uint64, n int) {
	if n > 0 {
		v.Add(uint64(n))
	}
}

func (s *Service) Sources() []metrics.Source {
	out := []metrics.Source{
		{Name: "typing_received_total", Help: "Typing frames from clients and typing signals from other nodes, before any drop.", Read: counterOf(&s.stats.received)},
		{Name: "typing_delivered_total", Help: "Typing notices queued to room viewers on this node.", Read: counterOf(&s.stats.delivered)},
		{Name: "typing_relay_failures_total", Help: "Typing signals that could not be published to the other nodes.", Read: counterOf(&s.stats.relayFailures)},
	}
	for reason := range dropReasonCount {
		out = append(out, metrics.Source{
			Name: "typing_dropped_total", Help: "Typing frames, signals or notices dropped, by reason.",
			Labels: map[string]string{"reason": dropLabels[reason]}, Read: counterOf(&s.stats.dropped[reason]),
		})
	}
	return out
}

func counterOf(v *atomic.Uint64) func() float64 { return func() float64 { return float64(v.Load()) } }
