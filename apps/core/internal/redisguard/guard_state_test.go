package redisguard

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestDegradedFollowsFailuresAndRecovery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuard(t, &testlog.Sink{}, &now)
	if g.Degraded() {
		t.Fatal("fresh guard reports degraded")
	}
	_ = g.Do(t.Context(), "op", func(context.Context) error { return errBoom })
	if !g.Degraded() {
		t.Fatal("guard not degraded after a failed call")
	}
	now = now.Add(time.Second)
	if err := g.Do(t.Context(), "op", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("probe after cooldown = %v", err)
	}
	if g.Degraded() {
		t.Fatal("guard still degraded after a successful probe")
	}
}
