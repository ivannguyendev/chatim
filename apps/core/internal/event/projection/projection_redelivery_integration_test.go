package projection_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type flakyHandler struct {
	*fakeHandler
	failed map[uint64]bool
}

func (h *flakyHandler) Handle(ctx context.Context, evs []projection.Event) []error {
	errs := h.fakeHandler.Handle(ctx, evs)
	for i, ev := range evs {
		if seq := ev.Proto.GetSeq(); seq == 2 && !h.failed[seq] {
			h.failed[seq] = true
			errs[i] = errBoom
		}
	}
	return errs
}

func TestProjectionsConsumerRedeliversFromAFailedEventOnRealNATS(t *testing.T) {
	js, cfg := realEventStream(t)
	if err := projection.EnsureConsumer(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	events := []*chatimv1.Event{msgCreated(roomA, 0, 1, at(1)), msgCreated(roomA, 0, 2, at(2)), msgCreated(roomA, 0, 3, at(3))}
	publishAll(t, js, cfg, events)
	h := &flakyHandler{fakeHandler: newFakeHandler("flaky", keepAll), failed: make(map[uint64]bool)}
	c := built(projection.NewConsumer([]projection.Projection{h}, projection.NewSource(js, cfg), cfg, (&testlog.Sink{}).Logger()))(t)
	result := make(chan error, 1)
	go func() { result <- c.Run(t.Context()) }()
	awaitSettled(t, js, cfg, uint64(len(events)))
	if err := c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Run = %v", err)
	}
	var seen []uint64
	for _, call := range h.handled() {
		seen = append(seen, call...)
	}
	if !slices.Equal(seen, []uint64{1, 2, 3, 2}) || c.Failures() != 1 {
		t.Fatalf("handled %v with %d failures; want 1, 2, 3 then only the Nak'd 2 again after the 5s delay", seen, c.Failures())
	}
}
