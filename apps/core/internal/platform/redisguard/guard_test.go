package redisguard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	degradedMsg  = "probe degraded"
	recoveredMsg = "probe recovered"
)

var (
	errSkipped = errors.New("cooling down")
	errBoom    = errors.New("boom")
)

func newGuard(t *testing.T, sink *testlog.Sink, now *time.Time) *Guard {
	t.Helper()
	g, err := New(Config{
		Name: "probe", Timeout: time.Second, Cooldown: time.Second, Skipped: errSkipped,
		Degraded: degradedMsg, Recovered: recoveredMsg, Now: func() time.Time { return *now },
	}, sink.Logger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	good := Config{Name: "x", Timeout: time.Second, Cooldown: time.Second, Skipped: errSkipped, Degraded: "d", Recovered: "r"}
	for name, mutate := range map[string]func(*Config){
		"no name":      func(c *Config) { c.Name = "" },
		"no skip":      func(c *Config) { c.Skipped = nil },
		"no messages":  func(c *Config) { c.Degraded = "" },
		"zero timeout": func(c *Config) { c.Timeout = 0 },
		"zero cool":    func(c *Config) { c.Cooldown = 0 },
	} {
		c := good
		mutate(&c)
		if _, err := New(c, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestCheckClientRequiresContextTimeouts(t *testing.T) {
	plain := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer plain.Close()
	bounded := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", ContextTimeoutEnabled: true})
	defer bounded.Close()
	if err := CheckClient(nil, "x"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("CheckClient(nil) = %v", err)
	}
	if err := CheckClient(plain, "x"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("CheckClient(no context timeouts) = %v", err)
	}
	if err := CheckClient(bounded, "x"); err != nil {
		t.Errorf("CheckClient(bounded) = %v", err)
	}
}

func TestFailureSkipsCallsUntilCooldownThenProbeRecovers(t *testing.T) {
	sink, now := &testlog.Sink{}, time.Unix(1_700_000_000, 0)
	g := newGuard(t, sink, &now)
	ctx := t.Context()
	err := g.Do(ctx, "op", func(context.Context) error { return errBoom })
	if !errors.Is(err, errBoom) || !strings.HasPrefix(err.Error(), "probe op: ") {
		t.Fatalf("failing call = %v, want wrapped boom", err)
	}
	called := false
	if err := g.Do(ctx, "op", func(context.Context) error { called = true; return nil }); !errors.Is(err, errSkipped) || called {
		t.Fatalf("call during cooldown = %v (called %v), want skipped", err, called)
	}
	now = now.Add(time.Second)
	if err := g.Do(ctx, "op", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("probe after cooldown = %v", err)
	}
	if err := g.Do(ctx, "op", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("call after recovery = %v", err)
	}
	if d, r := sink.Count(degradedMsg), sink.Count(recoveredMsg); d != 1 || r != 1 {
		t.Fatalf("logged degraded %d and recovered %d times, want 1 each", d, r)
	}
}

func TestCallBoundedByTimeoutAndCallerCancelIsNotAFailure(t *testing.T) {
	sink, now := &testlog.Sink{}, time.Unix(1_700_000_000, 0)
	g := newGuard(t, sink, &now)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.Do(ctx, "op", func(c context.Context) error { return c.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller = %v", err)
	}
	if err := g.Do(t.Context(), "op", func(c context.Context) error {
		if _, ok := c.Deadline(); !ok {
			return errors.New("no deadline")
		}
		return nil
	}); err != nil {
		t.Fatalf("call after caller cancel = %v, want admitted with a deadline", err)
	}
	if n := sink.Count(degradedMsg); n != 0 {
		t.Fatalf("caller cancellation logged degraded %d times", n)
	}
}

func TestSuccessStartedBeforeTheFailureDoesNotEndTheEpisode(t *testing.T) {
	sink, now := &testlog.Sink{}, time.Unix(1_700_000_000, 0)
	g := newGuard(t, sink, &now)
	ctx := t.Context()
	early, _ := g.admit(now)
	failing, _ := g.admit(now)
	g.observe(ctx, failing, "op", errBoom, now)
	g.observe(ctx, early, "op", nil, now)
	if _, ok := g.admit(now); ok {
		t.Fatal("a success that started before the failure ended the cooldown")
	}
	later := now.Add(time.Second)
	probe, ok := g.admit(later)
	if !ok {
		t.Fatal("no probe admitted after the cooldown")
	}
	if _, ok := g.admit(later); ok {
		t.Fatal("a second probe was admitted in the same cooldown")
	}
	g.observe(ctx, probe, "op", nil, later)
	if _, ok := g.admit(later); !ok {
		t.Fatal("redis still skipped after a successful probe")
	}
	if d, r := sink.Count(degradedMsg), sink.Count(recoveredMsg); d != 1 || r != 1 {
		t.Fatalf("logged degraded %d and recovered %d times, want 1 each", d, r)
	}
}
