package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var (
	activityAt     = time.UnixMilli(1_759_600_000_000).UTC()
	errTouchFailed = errors.New("touch failed")
)

type touchSpy struct {
	calls [][]store.Activity
	err   error
}

func (s *touchSpy) TouchActivity(_ context.Context, acts []store.Activity) error {
	s.calls = append(s.calls, slices.Clone(acts))
	return s.err
}

func activityRecord(room, thread, seq uint64, at time.Time) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: room, Thread: thread, Seq: seq, CommittedAt: at}
}

func TestRoomActivityTouchesTheHighestSeqPerTimelineInOneWrite(t *testing.T) {
	spy := &touchSpy{}
	fx := effects.NewRoomActivity(spy).Effect()
	if effects.RoomActivityName != "room_activity" || fx.Name != effects.RoomActivityName || fx.Delay != 0 {
		t.Fatalf("effect = %q with delay %v, want room_activity with delay 0", fx.Name, fx.Delay)
	}
	t1, t2, t3 := activityAt, activityAt.Add(time.Second), activityAt.Add(2*time.Second)
	recs := []work.Record{
		activityRecord(101, 0, 3, t1), activityRecord(101, 0, 5, t2), activityRecord(101, 7, 2, t1),
		activityRecord(202, 0, 1, t3), activityRecord(101, 0, 4, t3),
	}
	errs := fx.Run(t.Context(), recs)
	if len(errs) != len(recs) || slices.ContainsFunc(errs, func(err error) bool { return err != nil }) {
		t.Fatalf("Run errors = %v, want %d nils", errs, len(recs))
	}
	want := []store.Activity{{Room: 101, Seq: 5, At: t3}, {Room: 101, Thread: 7, Seq: 2, At: t1}, {Room: 202, Seq: 1, At: t3}}
	if len(spy.calls) != 1 || !slices.Equal(spy.calls[0], want) {
		t.Fatalf("TouchActivity calls = %+v, want one call with %+v", spy.calls, want)
	}
}

func TestRoomActivityFailsEveryRecordWhenTheWriteFails(t *testing.T) {
	spy := &touchSpy{err: errTouchFailed}
	recs := []work.Record{activityRecord(101, 0, 1, activityAt), activityRecord(202, 0, 1, activityAt)}
	errs := effects.NewRoomActivity(spy).Effect().Run(t.Context(), recs)
	if len(errs) != len(recs) {
		t.Fatalf("Run returned %d errors for %d records", len(errs), len(recs))
	}
	for i, err := range errs {
		if !errors.Is(err, errTouchFailed) {
			t.Errorf("errs[%d] = %v, want %v", i, err, errTouchFailed)
		}
	}
}

func TestRoomActivityNeverMovesAStoredRoomBack(t *testing.T) {
	rooms := memstore.NewRooms()
	r := domain.Room{ID: 101, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: activityAt, MemberCount: 1}
	owner := domain.Member{Room: 101, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: activityAt}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fx := effects.NewRoomActivity(rooms).Effect()
	for _, rec := range []work.Record{activityRecord(101, 0, 9, activityAt.Add(time.Minute)), activityRecord(101, 0, 4, activityAt.Add(2*time.Minute))} {
		if errs := fx.Run(t.Context(), []work.Record{rec}); errs[0] != nil {
			t.Fatalf("Run(%+v) = %v", rec, errs[0])
		}
	}
	got, err := rooms.Get(t.Context(), 101)
	if err != nil || got.LastSeq != 9 || !got.LastMsgAt.Equal(activityAt.Add(2*time.Minute)) {
		t.Fatalf("room = %+v, %v; want last seq 9 at %v", got, err, activityAt.Add(2*time.Minute))
	}
}
