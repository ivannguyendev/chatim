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

func mustMark(t *testing.T, marks *publish.ActivityMarks, room, last uint64) {
	t.Helper()
	if err := marks.Mark(t.Context(), room, last); err != nil {
		t.Fatalf("Mark(%d, %d): %v", room, last, err)
	}
}

func TestMarkAddsTheRoomToItsSlotSet(t *testing.T) {
	mr, rdb := newRedis(t)
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second}, nil)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	now := time.Now().UnixMilli()
	mustMark(t, marks, roomA, 0)
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
	mustMark(t, marks, roomA, 0)
	if members, _ := mr.ZMembers(key); len(members) != 1 {
		t.Fatalf("members after two marks = %v, want one", members)
	}
}

func TestMarkPinsTheGivenWatermarkWhenTheRoomHasNone(t *testing.T) {
	mr, rdb := newRedis(t)
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second}, nil)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	mustMark(t, marks, roomA, 7)
	if got, ok := watermark(mr, roomA); !ok || got != 7 {
		t.Fatalf("watermark after the first mark = %d (present %v), want 7", got, ok)
	}
	if ttl := mr.TTL(publish.WatermarkKey(roomA)); ttl != publish.DefaultWatermarkTTL {
		t.Fatalf("pinned watermark ttl = %v, want %v", ttl, publish.DefaultWatermarkTTL)
	}
}

func TestMarkNeverLowersOrOverwritesAnExistingWatermark(t *testing.T) {
	mr, rdb := newRedis(t)
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second, WatermarkTTL: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	mustSet(t, mr, publish.WatermarkKey(roomA), "9")
	mr.SetTTL(publish.WatermarkKey(roomA), time.Minute)
	mustMark(t, marks, roomA, 3)
	mustMark(t, marks, roomA, 20)
	if got, ok := watermark(mr, roomA); !ok || got != 9 {
		t.Fatalf("watermark after marks = %d (present %v), want it kept at 9", got, ok)
	}
	if ttl := mr.TTL(publish.WatermarkKey(roomA)); ttl != time.Hour {
		t.Fatalf("watermark ttl after a mark = %v, want it refreshed to %v", ttl, time.Hour)
	}
}

func TestMarkFailsFastWhileRedisIsDown(t *testing.T) {
	mr, rdb := newRedis(t)
	sink := &testlog.Sink{}
	marks, err := publish.NewActivityMarks(rdb, publish.MarkConfig{Timeout: time.Second, Cooldown: 20 * time.Millisecond}, sink.Logger())
	if err != nil {
		t.Fatalf("NewActivityMarks: %v", err)
	}
	mr.Close()
	for range 3 {
		if err := marks.Mark(t.Context(), roomA, 0); err == nil {
			t.Fatal("Mark succeeded with redis down")
		}
	}
	if n := sink.Count(marksDegradedMsg); n != 1 {
		t.Fatalf("logged degraded %d times, want 1", n)
	}
	if err := mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	eventually(t, "mark after redis returns", func() bool { return marks.Mark(t.Context(), roomA, 0) == nil })
	if n := sink.Count(marksRecoveredMsg); n != 1 {
		t.Fatalf("logged recovered %d times, want 1", n)
	}
}

func TestNewActivityMarksRejectsAMissingClientAndATinyTTL(t *testing.T) {
	if _, err := publish.NewActivityMarks(nil, publish.MarkConfig{}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewActivityMarks(nil) = %v", err)
	}
	_, rdb := newRedis(t)
	if _, err := publish.NewActivityMarks(rdb, publish.MarkConfig{WatermarkTTL: time.Microsecond}, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewActivityMarks(1µs ttl) = %v", err)
	}
}
