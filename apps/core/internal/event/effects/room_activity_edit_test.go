package effects_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestRoomActivityKeepsEditsApartFromOtherChanges(t *testing.T) {
	spy := &touchSpy{}
	t1, t2, t3 := activityAt, activityAt.Add(time.Second), activityAt.Add(2*time.Second)
	recs := []work.Record{
		{Kind: store.EditInserted, Room: 101, Seq: 5, Version: 1, CommittedAt: t1},
		{Kind: store.ReactionChanged, Room: 101, Seq: 5, Version: 2, User: "bob", CommittedAt: t2},
		{Kind: store.RoomChanged, Room: 101, Version: 2, CommittedAt: t3},
		{Kind: store.PreferenceChanged, Room: 202, Version: 1, User: "bob", CommittedAt: t1},
		{Kind: store.EditInserted, Room: 101, Thread: 7, Seq: 2, Version: 1, CommittedAt: t3},
	}
	errs := effects.NewRoomActivity(spy).Effect().Run(t.Context(), recs)
	if slices.ContainsFunc(errs, func(err error) bool { return err != nil }) {
		t.Fatalf("Run errors = %v, want nils", errs)
	}
	want := []store.Activity{{Room: 101, At: t1, Edited: true}, {Room: 101, At: t3}, {Room: 202, At: t1}, {Room: 101, Thread: 7, At: t3}}
	if len(spy.calls) != 1 || !slices.Equal(spy.calls[0], want) {
		t.Fatalf("TouchActivity calls = %+v, want %+v (only main-timeline edits raise the edit time)", spy.calls, want)
	}
}

func TestRoomActivityRaisesTheEditTimeOnlyForEdits(t *testing.T) {
	rooms := memstore.NewRooms()
	r := domain.Room{ID: 101, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: activityAt, MemberCount: 1}
	owner := domain.Member{Room: 101, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: activityAt}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fx := effects.NewRoomActivity(rooms).Effect()
	since := time.Now().Truncate(time.Millisecond)
	edit := work.Record{Kind: store.EditInserted, Room: 101, Seq: 1, Version: 1, CommittedAt: activityAt.Add(time.Minute)}
	react := work.Record{Kind: store.ReactionChanged, Room: 101, Seq: 1, Version: 1, User: "bob", CommittedAt: activityAt.Add(2 * time.Minute)}
	for _, rec := range []work.Record{edit, react} {
		if errs := fx.Run(t.Context(), []work.Record{rec}); errs[0] != nil {
			t.Fatalf("Run(%+v) = %v", rec, errs[0])
		}
	}
	got, err := rooms.Get(t.Context(), 101)
	if err != nil || got.LastEditAt.Before(since) || got.LastChangeAt.Before(got.LastEditAt) {
		t.Fatalf("room = %+v, %v; want last edit stamped by the store since %v and last change not before it", got, err, since)
	}
}
