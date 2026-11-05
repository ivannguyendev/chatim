package app

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

type closeCounter struct {
	idle
	closed *atomic.Int32
}

func (c closeCounter) Close(context.Context) error {
	c.closed.Add(1)
	return nil
}

func TestFailedStartClosesTheMentionAppAndStopsTheWatches(t *testing.T) {
	var closed atomic.Int32
	watch := &fakeWatch{}
	a := &node{log: quiet, mention: closeCounter{closed: &closed}, watches: liveWatchers{watch}}
	refused := errors.New("prepare refused")
	err := a.launch(t.Context(), func(context.Context) ([]net.Listener, error) { return nil, refused })
	if !errors.Is(err, refused) {
		t.Fatalf("launch = %v, want the prepare error", err)
	}
	if closed.Load() != 1 || !watch.stopped.Load() {
		t.Fatalf("mention closed %d times, watch stopped %v; want one close and a stop", closed.Load(), watch.stopped.Load())
	}
}

func TestFailedStartWithoutMentionStopsOnlyTheWatches(t *testing.T) {
	watch := &fakeWatch{}
	a := &node{log: quiet, watches: liveWatchers{watch}}
	if err := a.launch(t.Context(), func(context.Context) ([]net.Listener, error) { return nil, errors.New("listen") }); err == nil {
		t.Fatal("launch = nil, want the listen error")
	}
	if !watch.stopped.Load() {
		t.Fatal("watch not stopped after a failed start")
	}
}
