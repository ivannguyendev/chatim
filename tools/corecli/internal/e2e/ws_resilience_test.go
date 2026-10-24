package e2e_test

import (
	"testing"
	"testing/synctest"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	expired     = chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_EXPIRED
	unsupported = chatimv1.FrameErrorReason_FRAME_ERROR_REASON_UNSUPPORTED
	invalid     = chatimv1.FrameErrorReason_FRAME_ERROR_REASON_INVALID
)

func TestWSFeedRunRecordsAuthRequiredFrameErrorsAndRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startFeed(t)
		r.send(
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_AuthRequired{AuthRequired: &chatimv1.AuthRequiredFrame{Reason: expired}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_FrameError{FrameError: &chatimv1.FrameErrorFrame{Reason: unsupported}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_FrameError{FrameError: &chatimv1.FrameErrorFrame{Reason: invalid}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_RetryLater{RetryLater: &chatimv1.RetryLaterFrame{RetryAfterMs: 5000}}},
		)
		checks := []e2e.WSMatch{
			e2e.AuthRequiredMatch(expired),
			e2e.FrameErrorMatch(unsupported),
			e2e.FrameErrorMatch(invalid),
			{Frame: e2e.FrameRetryLater, Kind: "5s"},
		}
		for want, m := range checks {
			if i, _, ok := r.feed.Find(0, m); !ok || i != want {
				t.Fatalf("Find(%v) = %d, %v, want %d", m, i, ok, want)
			}
		}
		if _, _, ok := r.feed.Find(0, e2e.AuthRequiredMatch(chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_INVALID)); ok {
			t.Fatal("auth_required EXPIRED matched INVALID")
		}
	})
}

func TestCreatedMatchFindsTheMessageOfOneSeq(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Event(created("7", 2))
	if _, _, ok := f.Find(0, e2e.CreatedMatch("7", 2)); !ok {
		t.Fatal("seq 2 of room 7 not matched")
	}
	for _, m := range []e2e.WSMatch{e2e.CreatedMatch("7", 1), e2e.CreatedMatch("8", 2)} {
		if _, _, ok := f.Find(0, m); ok {
			t.Fatalf("%v matched seq 2 of room 7", m)
		}
	}
}

func TestCheckSilentWhileWaitingAllowsOnlyAuthRequired(t *testing.T) {
	waiting := e2e.WSEntry{Frame: e2e.FrameAuthRequired, Kind: expired.String()}
	if err := e2e.CheckSilentWhileWaiting([]e2e.WSEntry{waiting, waiting}); err != nil {
		t.Fatalf("auth_required only: %v", err)
	}
	if err := e2e.CheckSilentWhileWaiting(nil); err != nil {
		t.Fatalf("nothing: %v", err)
	}
	for _, other := range []e2e.WSEntry{
		{Frame: e2e.FrameEvent, Kind: "message_created", Room: "7", Seq: 2},
		{Frame: e2e.FrameNotification, Kind: "mention_notified", Room: "7"},
		{Frame: e2e.FrameReady},
		{Frame: e2e.FramePresence, Kind: "online", User: "b"},
	} {
		if err := e2e.CheckSilentWhileWaiting([]e2e.WSEntry{waiting, other}); err == nil {
			t.Fatalf("%+v while waiting accepted", other)
		}
	}
}

func TestCheckMissedWhileWaitingWantsOneGapOfTheMissedSeq(t *testing.T) {
	gap := e2e.WSEntry{Frame: e2e.FrameGap, Room: "7", Seq: 2, To: 2}
	next := e2e.WSEntry{Frame: e2e.FrameEvent, Kind: "message_created", ID: e2e.MessageEventID("7", 3), Room: "7", Seq: 3}
	if err := e2e.CheckMissedWhileWaiting([]e2e.WSEntry{{Frame: e2e.FrameReady}, gap, next}, "7", 2); err != nil {
		t.Fatalf("gap 2..2 then seq 3: %v", err)
	}
	cases := map[string][]e2e.WSEntry{
		"no gap":          {next},
		"wider gap":       {{Frame: e2e.FrameGap, Room: "7", Seq: 1, To: 2}, next},
		"gap of another":  {{Frame: e2e.FrameGap, Room: "8", Seq: 2, To: 2}, next},
		"missed seq seen": {{Frame: e2e.FrameEvent, Kind: "message_created", ID: e2e.MessageEventID("7", 2), Room: "7", Seq: 2}, gap, next},
		"no next seq":     {gap},
	}
	for name, entries := range cases {
		if err := e2e.CheckMissedWhileWaiting(entries, "7", 2); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestCheckClosedWithWantsTheCloseCode(t *testing.T) {
	if err := e2e.CheckClosedWith("the 11th", 4009, true, 4009); err != nil {
		t.Fatalf("4009: %v", err)
	}
	if err := e2e.CheckClosedWith("the 11th", 1000, true, 4009); err == nil {
		t.Fatal("1000 accepted for 4009")
	}
	if err := e2e.CheckClosedWith("the 11th", 0, false, 4009); err == nil {
		t.Fatal("a connection ended without a close frame accepted")
	}
}
