package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func mentionListCases() []mentionCase {
	return []mentionCase{
		{"ListByTargetGivesThatTargetNewestFirst", listByTargetNewestFirst},
		{"ListByTargetContinuesAfterTheCursorAcrossTies", listByTargetCursorTies},
		{"ListSharedKeepsAllAndMyGroupsAfterEachRoomsSince", listSharedSince},
		{"ListSharedMergesRoomsAndContinuesAfterTheCursor", listSharedCursor},
		{"ListRejectsABadQuery", listInvalid},
	}
}

func placed(t *testing.T, s store.Mentions, room, seq uint64, at time.Time, targets ...domain.MentionTarget) store.MsgKey {
	t.Helper()
	set := store.MentionSet{Key: store.MsgKey{Room: room, Seq: seq}, Tenant: tenant, Sender: "alice", Targets: targets, CreatedAt: at, At: at}
	mustApplyMentions(t, s, set)
	return set.Key
}

func mentionKeys(t *testing.T, got []domain.Mention, err error) []store.MsgKey {
	t.Helper()
	if err != nil {
		t.Fatalf("list mentions: %v", err)
	}
	out := make([]store.MsgKey, len(got))
	for i, m := range got {
		out[i] = store.MsgKey(m.Key)
	}
	return out
}

func byTarget(t *testing.T, s store.Mentions, q store.TargetMentionQuery) []store.MsgKey {
	t.Helper()
	got, err := s.ListByTarget(t.Context(), q)
	for _, m := range got {
		if !m.Live || m.Tenant != q.Tenant || m.Target.Name(m.Key.Room) != q.Target || !q.Before.Holds(m) {
			t.Fatalf("ListByTarget returned %+v, outside the query", m)
		}
	}
	return mentionKeys(t, got, err)
}

func shared(t *testing.T, s store.Mentions, q store.SharedMentionQuery) []store.MsgKey {
	t.Helper()
	got, err := s.ListShared(t.Context(), q)
	for _, m := range got {
		if !q.Wants(m) {
			t.Fatalf("ListShared returned %+v, outside the query", m)
		}
	}
	return mentionKeys(t, got, err)
}

func listByTargetNewestFirst(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionMinh)
	b1 := placed(t, s, roomB, 1, baseTime.Add(time.Second), mentionMinh, mentionLan)
	placed(t, s, roomA, 2, baseTime.Add(2*time.Second), mentionAll, mentionOps)
	a3 := placed(t, s, roomA, 3, baseTime.Add(3*time.Second), mentionMinh)
	gone := placed(t, s, roomA, 4, baseTime.Add(4*time.Second), mentionMinh)
	mustApplyMentions(t, s, store.MentionSet{Key: gone, Tenant: tenant, Sender: "alice", Ver: 1, CreatedAt: baseTime.Add(4 * time.Second), At: baseTime.Add(time.Hour)})
	other := store.MentionSet{Key: store.MsgKey{Room: roomB, Seq: 9}, Tenant: "globex", Sender: "alice", Targets: []domain.MentionTarget{mentionMinh}, CreatedAt: baseTime.Add(5 * time.Second), At: baseTime}
	mustApplyMentions(t, s, other)
	q := store.TargetMentionQuery{Tenant: tenant, Target: mentionMinh.Name(0), Limit: 10}
	if got, want := byTarget(t, s, q), []store.MsgKey{a3, b1, a1}; !slices.Equal(got, want) {
		t.Fatalf("ListByTarget = %v, want %v", got, want)
	}
	q.Limit = 2
	if got, want := byTarget(t, s, q), []store.MsgKey{a3, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListByTarget(limit 2) = %v, want %v", got, want)
	}
}

func listByTargetCursorTies(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionMinh)
	a2 := placed(t, s, roomA, 2, baseTime, mentionMinh)
	a3 := placed(t, s, roomA, 3, baseTime, mentionMinh)
	newer := placed(t, s, roomA, 4, baseTime.Add(time.Millisecond), mentionMinh)
	q := store.TargetMentionQuery{Tenant: tenant, Target: mentionMinh.Name(0), Limit: 3}
	if got, want := byTarget(t, s, q), []store.MsgKey{newer, a3, a2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	q.Before = store.MentionCursor{At: baseTime, Key: a2}
	if got, want := byTarget(t, s, q), []store.MsgKey{a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", a2, got, want)
	}
}

func listSharedSince(t *testing.T, s store.Mentions) {
	hanoi := domain.MentionTarget{Kind: domain.MentionGroup, ID: "hanoi"}
	placed(t, s, roomA, 1, baseTime, mentionAll)
	a2 := placed(t, s, roomA, 2, baseTime.Add(2*time.Second), mentionAll, mentionMinh)
	a3 := placed(t, s, roomA, 3, baseTime.Add(3*time.Second), mentionOps)
	placed(t, s, roomA, 4, baseTime.Add(4*time.Second), hanoi, mentionMinh)
	b1 := placed(t, s, roomB, 1, baseTime.Add(time.Second), mentionOps, mentionAll)
	placed(t, s, roomC, 1, baseTime.Add(5*time.Second), mentionAll)
	gone := placed(t, s, roomB, 2, baseTime.Add(6*time.Second), mentionAll)
	mustApplyMentions(t, s, store.MentionSet{Key: gone, Tenant: tenant, Sender: "alice", Ver: 1, CreatedAt: baseTime.Add(6 * time.Second), At: baseTime.Add(time.Hour)})
	rooms := []store.SharedRoom{{Room: roomA, Since: baseTime}, {Room: roomB}}
	q := store.SharedMentionQuery{Rooms: rooms, Groups: []string{mentionOps.ID}, Limit: 10}
	if got, want := shared(t, s, q), []store.MsgKey{a3, a2, b1, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListShared = %v, want %v", got, want)
	}
	q.Groups = nil
	if got, want := shared(t, s, q), []store.MsgKey{a2, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListShared(no groups) = %v, want %v", got, want)
	}
	q.Rooms = []store.SharedRoom{{Room: roomA, Since: baseTime.Add(2 * time.Second)}}
	if got := shared(t, s, q); len(got) != 0 {
		t.Fatalf("ListShared(since the last @all) = %v, want nothing", got)
	}
}

func listSharedCursor(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionAll)
	b1 := placed(t, s, roomB, 1, baseTime, mentionAll)
	a2 := placed(t, s, roomA, 2, baseTime.Add(time.Second), mentionAll)
	c1 := placed(t, s, roomC, 1, baseTime.Add(2*time.Second), mentionOps)
	q := store.SharedMentionQuery{Rooms: []store.SharedRoom{{Room: roomA}, {Room: roomB}, {Room: roomC}}, Groups: []string{mentionOps.ID}, Limit: 2}
	if got, want := shared(t, s, q), []store.MsgKey{c1, a2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	q.Before = store.MentionCursor{At: baseTime.Add(time.Second), Key: a2}
	if got, want := shared(t, s, q), []store.MsgKey{b1, a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", a2, got, want)
	}
	q.Before = store.MentionCursor{At: baseTime, Key: b1}
	if got, want := shared(t, s, q), []store.MsgKey{a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", b1, got, want)
	}
}

func listInvalid(t *testing.T, s store.Mentions) {
	good := store.TargetMentionQuery{Tenant: tenant, Target: mentionMinh.Name(0), Limit: 10}
	for name, edit := range map[string]func(*store.TargetMentionQuery){
		"no tenant":       func(q *store.TargetMentionQuery) { q.Tenant = "" },
		"no target":       func(q *store.TargetMentionQuery) { q.Target = "" },
		"zero limit":      func(q *store.TargetMentionQuery) { q.Limit = 0 },
		"limit over page": func(q *store.TargetMentionQuery) { q.Limit = store.MaxPageLimit + 1 },
		"cursor no key":   func(q *store.TargetMentionQuery) { q.Before = store.MentionCursor{At: baseTime} },
	} {
		q := good
		edit(&q)
		if _, err := s.ListByTarget(t.Context(), q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ListByTarget = %v, want ErrInvalidArgument", name, err)
		}
	}
	goodShared := store.SharedMentionQuery{Rooms: []store.SharedRoom{{Room: roomA}}, Limit: 10}
	for name, edit := range map[string]func(*store.SharedMentionQuery){
		"no rooms":       func(q *store.SharedMentionQuery) { q.Rooms = nil },
		"duplicate room": func(q *store.SharedMentionQuery) { q.Rooms = []store.SharedRoom{{Room: roomA}, {Room: roomA}} },
		"bad group":      func(q *store.SharedMentionQuery) { q.Groups = []string{""} },
		"zero limit":     func(q *store.SharedMentionQuery) { q.Limit = 0 },
	} {
		q := goodShared
		edit(&q)
		if _, err := s.ListShared(t.Context(), q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ListShared = %v, want ErrInvalidArgument", name, err)
		}
	}
}
