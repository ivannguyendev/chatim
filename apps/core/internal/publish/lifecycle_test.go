package publish_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEnqueueNeverBlocksWhenTheQueueIsFull(t *testing.T) {
	cfg := fastSetup
	cfg.Shards, cfg.QueueSize = 1, 2
	rg := newRig(t, cfg)
	rg.enqueue(t, roomA, 1)
	rg.enqueue(t, roomA, 2)
	begin := time.Now()
	for range 3 {
		if err := rg.Enqueue(roomA, events(roomA, 3)); !errors.Is(err, publish.ErrQueueFull) {
			t.Fatalf("Enqueue on a full queue = %v, want ErrQueueFull", err)
		}
	}
	if d := time.Since(begin); d > 100*time.Millisecond {
		t.Fatalf("full-queue Enqueue took %v", d)
	}
	if n := rg.sink.Count(queueFullMsg); n != 1 {
		t.Fatalf("logged a full queue %d times in one episode, want 1", n)
	}
	if err := rg.Enqueue(roomA, nil); err != nil {
		t.Fatalf("Enqueue with no events = %v", err)
	}
	if err := rg.Close(t.Context()); err != nil {
		t.Fatalf("Close before Run = %v", err)
	}
	if err := rg.Enqueue(roomA, events(roomA, 4)); !errors.Is(err, publish.ErrClosed) {
		t.Fatalf("Enqueue after Close = %v, want ErrClosed", err)
	}
}

func TestCloseDrainsQueuedEvents(t *testing.T) {
	rg := started(t, fastSetup)
	rg.js.Hold()
	rg.enqueue(t, roomA, 1, 2, 3)
	eventually(t, "three publishes in flight", func() bool { return rg.js.Held() == 3 })
	closed := make(chan error, 1)
	go func() { closed <- rg.Close(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	rg.js.Release()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := rg.wait(); err != nil {
		t.Fatalf("Run after Close = %v", err)
	}
	if n := len(rg.js.Stored()); n != 3 {
		t.Fatalf("stored %d events after Close, want 3", n)
	}
}

func TestCloseStopsAtItsDeadlineAndKeepsAckedProgress(t *testing.T) {
	cfg := fastSetup
	cfg.AckTimeout = time.Hour
	rg := started(t, cfg)
	rg.enqueue(t, roomA, 1)
	eventually(t, "seq 1 stored", func() bool { return len(rg.js.Stored()) == 1 })
	rg.js.Hold()
	rg.enqueue(t, roomA, 2)
	eventually(t, "seq 2 in flight", func() bool { return rg.js.Held() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := rg.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close with an unacked publish = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Close took %v past its 50ms deadline", d)
	}
	if got := storedIDs(rg.js); !slices.Equal(got, []string{"101-0-1"}) {
		t.Fatalf("stored %v after an aborted drain, want only 101-0-1", got)
	}
}

func TestHardCancelStopsShardsWithoutLeaks(t *testing.T) {
	rg := newRig(t, fastSetup)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rg.Run(ctx) }()
	rg.js.Hold()
	rg.enqueue(t, roomA, 1, 2)
	rg.enqueue(t, roomB, 1)
	eventually(t, "publishes in flight", func() bool { return rg.js.Held() == 3 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if err := rg.Run(context.Background()); err == nil {
		t.Fatal("second Run succeeded")
	}
	if err := rg.Close(t.Context()); err != nil {
		t.Fatalf("Close after Run returned = %v", err)
	}
}

func TestNewRejectsMissingDependenciesAndBadConfig(t *testing.T) {
	js := &publishtest.JetStream{}
	if _, err := publish.New(nil, fastSetup, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("New(nil jetstream) = %v", err)
	}
	for name, mutate := range map[string]func(*publish.Config){
		"dotted root":     func(c *publish.Config) { c.SubjectRoot = "evt.x" },
		"empty root":      func(c *publish.Config) { c.SubjectRoot = "" },
		"negative queue":  func(c *publish.Config) { c.QueueSize = -1 },
		"too many shards": func(c *publish.Config) { c.Shards = 1025 },
		"backoff above cap": func(c *publish.Config) {
			c.RetryBackoff, c.MaxBackoff = time.Second, time.Millisecond
		},
	} {
		cfg := fastSetup
		mutate(&cfg)
		if _, err := publish.New(js, cfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := publish.New(js, publish.Config{SubjectRoot: "evt"}, nil); err != nil {
		t.Errorf("New with defaults = %v", err)
	}
	if n := len(publish.Config{}.JetStreamOptions()); n != 2 {
		t.Errorf("JetStreamOptions returned %d options, want 2", n)
	}
}
