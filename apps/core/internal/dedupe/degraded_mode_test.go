package dedupe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

func TestRedisFailureDegradesOnceAndProbesAfterCooldown(t *testing.T) {
	mr, rdb := newRedis(t)
	sink := &testlog.Sink{}
	s := newStore(t, rdb, "core-a", sink)
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	ctx := t.Context()
	reserve(t, s, key("warm"))

	mr.Close()
	if _, err := s.Reserve(ctx, []Key{key("a")}); err == nil || errors.Is(err, ErrDegraded) {
		t.Fatalf("Reserve with redis down = %v, want the redis error", err)
	}
	expectSkipped(t, s)
	now = now.Add(DefaultCooldown - time.Nanosecond)
	expectSkipped(t, s)
	now = now.Add(time.Nanosecond)
	if err := s.Commit(ctx, []Entry{{Key: key("a"), Record: sampleRecord}}); err == nil || errors.Is(err, ErrDegraded) {
		t.Fatalf("probe after cooldown = %v, want the redis error", err)
	}
	expectSkipped(t, s)
	if n := sink.Count(degradedMsg); n != 1 {
		t.Fatalf("logged degraded %d times during one outage, want 1", n)
	}

	if err := mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	var err error
	for range 5 {
		now = now.Add(DefaultCooldown)
		if _, err = s.Reserve(ctx, []Key{key("b")}); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("Reserve after redis returned: %v", err)
	}
	reserve(t, s, key("c"))
	if d, r := sink.Count(degradedMsg), sink.Count(recoveredMsg); d != 1 || r != 1 {
		t.Fatalf("logged degraded %d and recovered %d times, want 1 each", d, r)
	}
}

func TestCallerCancellationIsNotARedisFailure(t *testing.T) {
	_, rdb := newRedis(t)
	sink := &testlog.Sink{}
	s := newStore(t, rdb, "core-a", sink)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Reserve(ctx, []Key{key("a")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reserve with a cancelled caller = %v, want context.Canceled", err)
	}
	expectStatuses(t, reserve(t, s, key("a")), Reserved)
	if n := sink.Count(degradedMsg); n != 0 {
		t.Fatalf("caller cancellation logged degraded %d times", n)
	}
}

func expectSkipped(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	_, errReserve := s.Reserve(ctx, []Key{key("a")})
	errCommit := s.Commit(ctx, []Entry{{Key: key("a"), Record: sampleRecord}})
	errAbort := s.Abort(ctx, []Key{key("a")})
	for _, err := range []error{errReserve, errCommit, errAbort} {
		if !errors.Is(err, ErrDegraded) {
			t.Fatalf("call during cooldown = %v, want ErrDegraded", err)
		}
	}
}
