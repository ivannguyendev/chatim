package stateclear_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestAddDropsAndCountsWhenTheBufferIsFull(t *testing.T) {
	rg := newRig(t, smallBuffer)
	rg.add(1, 2, 3, 4)
	rg.expectCounts(t, 0, 2, 0)
	if n := rg.sink.Count(queueFullMsg); n != 1 {
		t.Fatalf("logged a full queue %d times, want 1", n)
	}
	if err := rg.Close(t.Context()); err != nil {
		t.Fatalf("Close before Run = %v", err)
	}
	expectSizes(t, rg.st)
	rg.expectCounts(t, 0, 4, 0)
}

func TestAddAfterCloseIsACountedDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.closeAndWait(t)
		rg.add(1, 2)
		rg.expectCounts(t, 0, 2, 0)
		if err := rg.Close(t.Context()); err != nil {
			t.Fatalf("second Close = %v", err)
		}
	})
}

func TestCloseDrainsTheRemainder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, stateclear.Config{Batch: 2, Queue: 8})
		rg.add(1, 2, 3, 4, 5)
		start := time.Now()
		rg.closeAndWait(t)
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("Close took %v with a healthy store, want no wait", elapsed)
		}
		expectSizes(t, rg.st, 2, 2, 1)
		rg.expectCounts(t, 5, 0, 0)
	})
}

func TestCloseGivesUpAfterTheDrainBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, stateclear.Config{Batch: 2, Queue: 8})
		rg.st.set(nil, true)
		rg.add(1, 2, 3, 4, 5)
		time.Sleep(500 * time.Millisecond)
		start := time.Now()
		rg.closeAndWait(t)
		if elapsed := time.Since(start); elapsed != stateclear.DrainTimeout {
			t.Fatalf("Close took %v, want %v", elapsed, stateclear.DrainTimeout)
		}
		expectSizes(t, rg.st, 2, 2)
		rg.expectCounts(t, 0, 1, 4)
	})
}

func TestCloseStopsTheDrainWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.st.set(nil, true)
		rg.add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := rg.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want DeadlineExceeded", err)
		}
		if err := <-rg.done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		rg.expectCounts(t, 0, 0, 1)
	})
}

func TestRunEndsWhenItsContextIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, defaultCfg)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { rg.done <- rg.Run(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-rg.done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want Canceled", err)
		}
		if err := rg.Close(t.Context()); err != nil {
			t.Fatalf("Close after Run ended = %v", err)
		}
	})
}

func TestRunStartsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		synctest.Wait()
		if err := rg.Run(context.Background()); err == nil {
			t.Fatal("second Run = nil, want an error")
		}
		rg.closeAndWait(t)
	})
}

func TestNewRejectsBadInput(t *testing.T) {
	if _, err := stateclear.New(nil, defaultCfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New without a store = %v, want ErrInvalidArgument", err)
	}
	if _, err := stateclear.New(&fakeStore{}, stateclear.Config{Batch: -1}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New with a bad config = %v, want ErrInvalidArgument", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  stateclear.Config
		ok   bool
	}{
		{"defaults", stateclear.Config{}, true},
		{"explicit", stateclear.Config{Flush: 50 * time.Millisecond, Batch: 100, Queue: 1000}, true},
		{"negative flush", stateclear.Config{Flush: -time.Millisecond}, false},
		{"negative batch", stateclear.Config{Batch: -1}, false},
		{"negative queue", stateclear.Config{Queue: -1}, false},
		{"batch above queue", stateclear.Config{Batch: 10, Queue: 5}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("Validate = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
