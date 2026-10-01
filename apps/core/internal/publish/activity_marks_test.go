package publish_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	marksDegradedMsg  = "active room marks degraded; sends continue without recovery marks"
	marksRecoveredMsg = "active room marks recovered"
)

func TestMarkActiveAddsTheRoomToItsSlotSet(t *testing.T) {
	mr, rdb := newRedis(t)
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second}, nil)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := marks.MarkActive(t.Context(), roomA); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	key := publish.ActiveKey(slotmap.Of(roomA))
	if key != "chatim:active:"+strconv.Itoa(int(slotmap.Of(roomA))) {
		t.Fatalf("active key = %q", key)
	}
	score, err := mr.ZScore(key, "101")
	if err != nil {
		t.Fatalf("ZScore: %v", err)
	}
	if ms := int64(score); ms < now-time.Second.Milliseconds() || ms > now+time.Second.Milliseconds() {
		t.Fatalf("score %d is not the mark time %d in unix milliseconds", ms, now)
	}
	if err := marks.MarkActive(t.Context(), roomA); err != nil {
		t.Fatalf("second MarkActive: %v", err)
	}
	if members, _ := mr.ZMembers(key); len(members) != 1 {
		t.Fatalf("members after two marks = %v, want one", members)
	}
}

func TestMarkActiveFailsFastWhileRedisIsDown(t *testing.T) {
	mr, rdb := newRedis(t)
	sink := &testlog.Sink{}
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second, Cooldown: 20 * time.Millisecond}, sink.Logger())
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	mr.Close()
	for range 3 {
		if err := marks.MarkActive(t.Context(), roomA); err == nil {
			t.Fatal("MarkActive succeeded with redis down")
		}
	}
	if n := sink.Count(marksDegradedMsg); n != 1 {
		t.Fatalf("logged degraded %d times, want 1", n)
	}
	if err := mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	eventually(t, "mark after redis returns", func() bool { return marks.MarkActive(t.Context(), roomA) == nil })
	if n := sink.Count(marksRecoveredMsg); n != 1 {
		t.Fatalf("logged recovered %d times, want 1", n)
	}
	if _, err := publish.NewActivityMarks(nil, publish.MarkConfig{}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewActivityMarks(nil) = %v", err)
	}
}
