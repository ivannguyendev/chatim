package work

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestArmMessageCountCheckPublishesOnItsOwnTimerSubject(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	key := store.MsgKey{Room: 777, Thread: 3, Seq: 9}
	timer, err := tm.ArmMessageCountCheck(t.Context(), key, "replies")
	if err != nil || timer != (Timer{Seq: 41}) {
		t.Fatalf("ArmMessageCountCheck = %+v, %v; want the PubAck sequence 41", timer, err)
	}
	if len(js.msgs) != 1 || js.msgs[0].Subject != "work.timer.777.3.9.replies.42" || js.optCounts[0] != 2 {
		t.Fatalf("published %d msgs, first on %q with %v options; want one on work.timer.777.3.9.replies.42 with the schedule time and target", len(js.msgs), js.msgs[0].Subject, js.optCounts)
	}
	fire := time.Date(2026, 10, 8, 10, 0, 6, 0, time.UTC)
	r, err := Decode(js.msgs[0].Data)
	want := Record{Kind: store.MessageCountCheck, Room: 777, Thread: 3, Seq: 9, Version: 42, User: "replies", CommittedAt: fire}
	if err != nil || r != want {
		t.Fatalf("payload = %+v, %v; want %+v", r, err, want)
	}
	if id := js.msgs[0].Header.Get(jetstream.MsgIDHeader); id != "q:777-3-9-replies-42" {
		t.Fatalf("Nats-Msg-Id = %q, want q:777-3-9-replies-42", id)
	}
	if _, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777, Seq: 9}, "reactions"); err != nil || js.msgs[1].Subject != "work.timer.777.0.9.reactions.42" {
		t.Fatalf("reactions timer = %v on %q, want work.timer.777.0.9.reactions.42", err, js.msgs[1].Subject)
	}
}

func TestMessageAndMemberTimerSubjectsNeverMeet(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	if _, err := tm.ArmMemberCountCheck(t.Context(), 777); err != nil {
		t.Fatalf("ArmMemberCountCheck: %v", err)
	}
	if _, err := tm.ArmMessageCountCheck(t.Context(), store.MsgKey{Room: 777, Seq: 42}, "reactions"); err != nil {
		t.Fatalf("ArmMessageCountCheck: %v", err)
	}
	if js.msgs[0].Subject == js.msgs[1].Subject {
		t.Fatalf("member and message timers share subject %q", js.msgs[0].Subject)
	}
}

func TestArmMessageCountCheckRefusesBadKeysAndCounters(t *testing.T) {
	js := &fakeTimerJS{}
	tm := newTestTimers(t, js, &testlog.Sink{})
	good := store.MsgKey{Room: 777, Seq: 9}
	cases := map[string]struct {
		key     store.MsgKey
		counter string
	}{
		"zero room":       {store.MsgKey{Seq: 9}, "reactions"},
		"zero seq":        {store.MsgKey{Room: 777}, "reactions"},
		"members counter": {good, "members"},
		"empty counter":   {good, ""},
		"dotted counter":  {good, "replies.x"},
	}
	for name, c := range cases {
		if _, err := tm.ArmMessageCountCheck(t.Context(), c.key, c.counter); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ArmMessageCountCheck = %v, want ErrInvalidArgument", name, err)
		}
	}
	js.pubErr = errNATS
	if _, err := tm.ArmMessageCountCheck(t.Context(), good, "replies"); !errors.Is(err, errNATS) {
		t.Fatalf("ArmMessageCountCheck with NATS down = %v, want the publish error", err)
	}
	if len(js.msgs) != 1 {
		t.Fatalf("published %d msgs, want only the attempt with NATS down", len(js.msgs))
	}
}
