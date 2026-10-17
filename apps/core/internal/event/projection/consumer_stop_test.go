package projection_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type expiringSource struct {
	mu    sync.Mutex
	held  []jsconsume.Delivery
	waits []time.Duration
}

func (s *expiringSource) Fetch(ctx context.Context, _ int, wait time.Duration) ([]jsconsume.Delivery, error) {
	s.mu.Lock()
	s.waits = append(s.waits, wait)
	s.mu.Unlock()
	time.Sleep(wait)
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.held
	s.held = nil
	return b, ctx.Err()
}

func (s *expiringSource) asked() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.waits)
}

func TestConsumerStopDrainsAFetchInFlightWithinTheProjectionsPhase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := delivery(t, msgCreated(roomA, 0, 1, at(1)), 1)
		src := &expiringSource{held: deliveries([]*fakeDelivery{in})}
		writer := &fakeWriter{}
		c := built(projection.NewConsumer([]projection.Projection{projection.NewRoomActivity(writer)}, src, evtConfig, (&testlog.Sink{}).Logger()))(t)
		result := make(chan error, 1)
		go func() { result <- c.Run(t.Context()) }()
		synctest.Wait()
		start := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), evtConfig.StopTimeout())
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Fatalf("Close within the %v projections phase = %v", evtConfig.StopTimeout(), err)
		}
		if took := time.Since(start); took != projection.DefaultFetchWait {
			t.Errorf("stop took %v, want the fetch read to its 3s server expiry", took)
		}
		if err := <-result; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if got := src.asked(); !slices.Equal(got, []time.Duration{3 * time.Second}) {
			t.Errorf("fetch waits %v, want one 3s pull", got)
		}
		if acks, _, naks := in.settled(); acks != 0 || !slices.Equal(naks, []time.Duration{0}) {
			t.Errorf("acks %d naks %v; want the event the expiring pull brought handed back with Nak(0)", acks, naks)
		}
		if len(writer.touched()) != 0 {
			t.Errorf("writes %v, want none after the stop", writer.touched())
		}
	})
}
