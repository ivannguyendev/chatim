package storetest

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func sharedMentionCases() []memberCase {
	return []memberCase{
		{"raising the shared mention time only moves it forward", sharedMentionRaises},
		{"shared mention times lists only rooms that had one, across chunks", sharedMentionTimes},
		{"shared mention calls reject bad input and missing rooms", sharedMentionInvalid},
	}
}

func mustRaiseShared(t *testing.T, s MemberRooms, room uint64, at time.Time) {
	t.Helper()
	if err := s.RaiseSharedMention(t.Context(), room, at); err != nil {
		t.Fatalf("RaiseSharedMention(%d, %v): %v", room, at, err)
	}
}

func sharedMentionRaises(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	mustRaiseShared(t, s, roomA, secs(20))
	mustRaiseShared(t, s, roomA, secs(10))
	got, err := s.Get(t.Context(), roomA)
	if err != nil || !got.LastSharedMentionAt.Equal(secs(20)) {
		t.Fatalf("Get after an older raise = %v, %v; want the shared mention time %v kept", got.LastSharedMentionAt, err, secs(20))
	}
	mustRaiseShared(t, s, roomA, secs(30))
	if got, err = s.Get(t.Context(), roomA); err != nil || !got.LastSharedMentionAt.Equal(secs(30)) || got.MemberCountVer != 1 {
		t.Fatalf("Get after a newer raise = %+v, %v; want shared mention time %v and member count ver 1", got, err, secs(30))
	}
}

func sharedMentionTimes(t *testing.T, s MemberRooms) {
	far := roomA + store.SharedMentionChunk + 5
	for _, id := range []uint64{roomA, roomB, far} {
		r, members := teamOf(id)
		mustCreate(t, s, r, members)
	}
	mustRaiseShared(t, s, roomA, secs(10))
	mustRaiseShared(t, s, far, secs(40))
	ids := make([]uint64, 0, store.SharedMentionChunk+10)
	for i := range uint64(store.SharedMentionChunk + 10) {
		ids = append(ids, roomA+i)
	}
	got, err := s.SharedMentionTimes(t.Context(), ids)
	want := map[uint64]time.Time{roomA: secs(10), far: secs(40)}
	if err != nil || !maps.EqualFunc(got, want, time.Time.Equal) {
		t.Fatalf("SharedMentionTimes = %v, %v; want %v", got, err, want)
	}
	if got, err := s.SharedMentionTimes(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("SharedMentionTimes(nil) = %v, %v; want empty", got, err)
	}
}

func sharedMentionInvalid(t *testing.T, s MemberRooms) {
	assertErrorIs(t, "RaiseSharedMention(room 0)", s.RaiseSharedMention(t.Context(), 0, secs(1)), apperr.ErrInvalidArgument)
	assertErrorIs(t, "RaiseSharedMention(zero time)", s.RaiseSharedMention(t.Context(), roomA, time.Time{}), apperr.ErrInvalidArgument)
	assertErrorIs(t, "RaiseSharedMention(missing room)", s.RaiseSharedMention(t.Context(), roomC, secs(1)), domain.ErrRoomNotFound)
	_, err := s.SharedMentionTimes(t.Context(), []uint64{roomA, 0})
	assertErrorIs(t, "SharedMentionTimes(room 0)", err, apperr.ErrInvalidArgument)
}
