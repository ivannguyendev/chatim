package dedupe

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestNewBatcherRejectsBadInput(t *testing.T) {
	if _, err := NewBatcher(nil, BatchConfig{}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewBatcher(nil store) = %v, want ErrInvalidArgument", err)
	}
	reg := &fakeRegistry{gate: make(chan struct{})}
	for _, cfg := range []BatchConfig{{Shards: -1}, {MaxKeys: -1}, {Queue: -1}, {Shards: slotmap.Count + 1}} {
		if _, err := NewBatcher(reg, cfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("NewBatcher(%+v) = %v, want ErrInvalidArgument", cfg, err)
		}
	}
	if err := (BatchConfig{}).Validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
	if n := (BatchConfig{}).Connections(); n != 2*DefaultBatchShards {
		t.Fatalf("Connections() = %d, want %d", n, 2*DefaultBatchShards)
	}
}

func TestRunTwiceIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, _, _ := startFake(t, testBatch, false)
		synctest.Wait()
		if err := b.Run(t.Context()); !errors.Is(err, errBatcherStarted) {
			t.Fatalf("second Run = %v, want errBatcherStarted", err)
		}
	})
}

func TestReserveCoalescesWaitingCallsIntoOneRound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		rooms := roomsOnShard(testBatch.Shards, 1, 11)
		first := reserveAsync(t.Context(), b, roomKeys(rooms[0], 1))
		synctest.Wait()
		waiting := make([]<-chan reserveResult, 0, 10)
		for _, room := range rooms[1:] {
			waiting = append(waiting, reserveAsync(t.Context(), b, roomKeys(room, 1)))
		}
		synctest.Wait()
		reg.open()
		expectVerdicts(t, <-first, roomKeys(rooms[0], 1))
		for i, w := range waiting {
			expectVerdicts(t, <-w, roomKeys(rooms[i+1], 1))
		}
		reserves, _, _ := reg.calls()
		if sizes := roundSizes(reserves); !slices.Equal(sizes, []int{1, 10}) {
			t.Fatalf("reserve rounds = %v keys, want [1 10]", sizes)
		}
	})
}

func TestReserveRoundsRespectMaxKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := BatchConfig{Shards: 1, MaxKeys: 4, Queue: 64}
		b, reg, _ := startFake(t, cfg, true)
		rooms := roomsOnShard(1, 0, 11)
		first := reserveAsync(t.Context(), b, roomKeys(rooms[0], 1))
		synctest.Wait()
		waiting := make([]<-chan reserveResult, 0, 10)
		for _, room := range rooms[1:] {
			waiting = append(waiting, reserveAsync(t.Context(), b, roomKeys(room, 1)))
		}
		synctest.Wait()
		reg.open()
		expectVerdicts(t, <-first, roomKeys(rooms[0], 1))
		for i, w := range waiting {
			expectVerdicts(t, <-w, roomKeys(rooms[i+1], 1))
		}
		big := roomKeys(rooms[0], 6)
		expectVerdicts(t, <-reserveAsync(t.Context(), b, big), big)
		reserves, _, _ := reg.calls()
		if sizes := roundSizes(reserves); !slices.Equal(sizes, []int{1, 4, 4, 2, 6}) {
			t.Fatalf("reserve rounds = %v keys, want [1 4 4 2 6]", sizes)
		}
	})
}

func TestReserveErrorReachesEveryCallerInTheRound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		reg.fail(ErrDegraded, false)
		rooms := roomsOnShard(testBatch.Shards, 0, 4)
		first := reserveAsync(t.Context(), b, roomKeys(rooms[0], 1))
		synctest.Wait()
		waiting := []<-chan reserveResult{first}
		for _, room := range rooms[1:] {
			waiting = append(waiting, reserveAsync(t.Context(), b, roomKeys(room, 2)))
		}
		synctest.Wait()
		reg.open()
		for i, w := range waiting {
			if got := <-w; !errors.Is(got.err, ErrDegraded) || got.verdicts != nil {
				t.Fatalf("caller %d got %v, %v, want ErrDegraded", i, got.verdicts, got.err)
			}
		}
		reg.fail(nil, true)
		got := <-reserveAsync(t.Context(), b, roomKeys(rooms[0], 2))
		if got.err == nil || got.verdicts != nil {
			t.Fatalf("short store answer = %v, %v, want an error", got.verdicts, got.err)
		}
	})
}

func TestReserveReturnsWhenTheCallerGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		room := roomsOnShard(testBatch.Shards, 0, 1)[0]
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if v, err := b.Reserve(ctx, roomKeys(room, 1)); !errors.Is(err, context.DeadlineExceeded) || v != nil {
			t.Fatalf("Reserve past the caller deadline = %v, %v, want DeadlineExceeded", v, err)
		}
		reg.open()
		keys := roomKeys(room, 3)
		expectVerdicts(t, <-reserveAsync(t.Context(), b, keys), keys)
		if v, err := b.Reserve(t.Context(), nil); v != nil || err != nil {
			t.Fatalf("Reserve(nil) = %v, %v", v, err)
		}
	})
}
