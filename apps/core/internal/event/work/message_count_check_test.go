package work_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMessageCountCheckBuildsTheTimerRecord(t *testing.T) {
	at := time.Date(2026, 10, 8, 10, 0, 6, 0, time.UTC)
	r, err := work.MessageCountCheck(store.MsgKey{Room: 777, Seq: 9}, "replies", 42, at)
	want := work.Record{Kind: store.MessageCountCheck, Room: 777, Seq: 9, Version: 42, User: "replies", CommittedAt: at}
	if err != nil || r != want || r.ID() != "q:777-0-9-replies-42" {
		t.Fatalf("MessageCountCheck = %+v (%s), %v; want %+v", r, r.ID(), err, want)
	}
	m := work.Message("work", 32, r)
	if m.Subject != work.Subject("work", work.Partition(777, 32)) {
		t.Fatalf("subject = %q, want the room partition", m.Subject)
	}
}

func TestMessageCountCheckRefusesBadKeysAndCounters(t *testing.T) {
	good := store.MsgKey{Room: 777, Seq: 9}
	for name, c := range map[string]struct {
		key     store.MsgKey
		counter string
	}{
		"zero room":       {store.MsgKey{Seq: 9}, "reactions"},
		"zero seq":        {store.MsgKey{Room: 777}, "reactions"},
		"thread":          {store.MsgKey{Room: 777, Thread: 3, Seq: 9}, "reactions"},
		"members counter": {good, "members"},
		"empty counter":   {good, ""},
	} {
		if _, err := work.MessageCountCheck(c.key, c.counter, 1, time.Now()); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: MessageCountCheck = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestRandomOpsDiffer(t *testing.T) {
	if a, b, c := work.RandomOp(), work.RandomOp(), work.RandomOp(); a == b && b == c {
		t.Fatalf("RandomOp returned %d three times", a)
	}
}
