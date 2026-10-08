package publish_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEnqueueNeverBlocksWhenTheQueueIsFull(t *testing.T) {
	cfg := fastSetup
	cfg.Shards, cfg.QueueSize = 1, 2
	rg := newRig(t, cfg)
	rg.enqueue(t, roomA, 1)
	rg.enqueue(t, roomA, 2)
	for range 3 {
		if err := rg.Enqueue(roomA, events(roomA, 3)); !errors.Is(err, publish.ErrQueueFull) {
			t.Fatalf("Enqueue on a full queue = %v, want ErrQueueFull", err)
		}
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

func TestCloseWaitsForInFlightPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1, 2, 3)
		synctest.Wait()
		if n := rg.js.Held(); n != 3 {
			t.Fatalf("%d publishes in flight before Close, want 3", n)
		}
		closed := make(chan error, 1)
		go func() { closed <- rg.Close(context.Background()) }()
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v before acks arrived", err)
		default:
		}
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
	})
}

func TestCloseStopsAtItsDeadlineWhenAcksDoNotArrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		rg.js.Hold()
		rg.enqueue(t, roomA, 2)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := rg.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close with an unacked publish = %v, want DeadlineExceeded", err)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"101-0-1"}) {
			t.Fatalf("stored %v after an aborted drain, want only 101-0-1", got)
		}
		rg.js.Release()
	})
}

func TestHardCancelStopsShardsWithoutLeaks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, fastSetup)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- rg.Run(ctx) }()
		rg.js.Hold()
		rg.enqueue(t, roomA, 1, 2)
		rg.enqueue(t, roomB, 1)
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v, want context.Canceled", err)
		}
		rg.js.Release()
		if err := rg.Run(context.Background()); err == nil {
			t.Fatal("second Run succeeded")
		}
		if err := rg.Close(t.Context()); err != nil {
			t.Fatalf("Close after Run returned = %v", err)
		}
	})
}

func TestNewRejectsMissingDependenciesAndBadConfig(t *testing.T) {
	js := &publishtest.JetStream{}
	if _, err := publish.New(nil, fastSetup, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("New(nil jetstream) = %v", err)
	}
	for name, mutate := range map[string]func(*publish.Config){
		"dotted root":      func(c *publish.Config) { c.SubjectRoot = "evt.x" },
		"empty root":       func(c *publish.Config) { c.SubjectRoot = "" },
		"negative queue":   func(c *publish.Config) { c.QueueSize = -1 },
		"too many shards":  func(c *publish.Config) { c.Shards = 1025 },
		"negative backoff": func(c *publish.Config) { c.RetryBackoff = -time.Millisecond },
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
	if n := len(publish.Config{}.JetStreamOptions(nil, nil)); n != 3 {
		t.Errorf("JetStreamOptions returned %d options, want 3", n)
	}
}
