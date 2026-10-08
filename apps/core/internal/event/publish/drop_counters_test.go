package publish_test

import (
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

func TestQueueFullDropsAreCounted(t *testing.T) {
	c := &publish.Counters{}
	cfg := fastSetup
	cfg.QueueSize = 1
	rg := newRig(t, cfg, publish.WithCounters(c))
	rg.enqueue(t, roomA, 1, 2)
	if err := rg.Enqueue(roomA, events(roomA, 3, 4, 5)); !errors.Is(err, publish.ErrQueueFull) {
		t.Fatalf("Enqueue on a full queue = %v, want ErrQueueFull", err)
	}
	if got := c.Drops().QueueFull; got != 3 {
		t.Fatalf("queue full drops = %d, want 3", got)
	}
}

func TestRefusedPublishesAreCounted(t *testing.T) {
	c := &publish.Counters{}
	rg := newRig(t, fastSetup, publish.WithCounters(c)).start(t)
	rg.js.RefuseWhen(func(*nats.Msg) error { return errRefused })
	rg.enqueue(t, roomA, 1, 2)
	eventually(t, "two refused publishes counted", func() bool { return c.Drops().Refused == 2 })
}

func TestAsyncFailuresAreCounted(t *testing.T) {
	c := &publish.Counters{}
	handle := publish.AsyncFailureHandler((&testlog.Sink{}).Logger(), c)
	msg := &nats.Msg{Subject: "evt.acme.message.101.msg_created", Header: nats.Header{}}
	msg.Header.Set(jetstream.MsgIDHeader, "101-0-1")
	for range 4 {
		handle(nil, msg, errRefused)
	}
	if got := c.Drops().AsyncFailed; got != 4 {
		t.Fatalf("async failures = %d, want 4", got)
	}
}

func TestFailedMarksAreCounted(t *testing.T) {
	c := &publish.Counters{}
	marker := &recordingMarker{err: errRefused}
	rg := newRig(t, fastSetup, publish.WithAckMarks(marker), publish.WithCounters(c)).start(t)
	rg.enqueue(t, roomA, 1, 2)
	eventually(t, "two failed marks counted", func() bool { return c.Drops().MarkFailed == 2 })
}
