package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
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

func placedIn(t *testing.T, s store.Mentions, ten string, room, seq uint64, at time.Time, targets ...domain.Target) domain.MessageKey {
	t.Helper()
	live := make([]domain.Target, len(targets))
	for i, x := range targets {
		live[i] = stamped(x, at)
	}
	p := planOf(keyOf(room, seq), 0, live, nil)
	p.Tenant = ten
	mustApply(t, s, p)
	return p.Key
}

func placed(t *testing.T, s store.Mentions, room, seq uint64, at time.Time, targets ...domain.Target) domain.MessageKey {
	t.Helper()
	return placedIn(t, s, tenant, room, seq, at, targets...)
}

func retire(t *testing.T, s store.Mentions, key domain.MessageKey, at time.Time, targets ...domain.Target) {
	t.Helper()
	gone := make([]domain.Target, len(targets))
	for i, x := range targets {
		gone[i] = stamped(x, at)
	}
	mustApply(t, s, planOf(key, 1, nil, gone))
}

func mentionKeys(t *testing.T, got []domain.Mention, err error) []domain.MessageKey {
	t.Helper()
	if err != nil {
		t.Fatalf("list mentions: %v", err)
	}
	out := make([]domain.MessageKey, len(got))
	for i, m := range got {
		out[i] = m.Key
	}
	return out
}

func byTarget(t *testing.T, s store.Mentions, q store.TargetMentionQuery) []domain.MessageKey {
	t.Helper()
	got, err := s.ListByTarget(t.Context(), q)
	for _, m := range got {
		if !q.Wants(m) {
			t.Fatalf("ListByTarget returned %+v, outside the query", m)
		}
	}
	return mentionKeys(t, got, err)
}

func shared(t *testing.T, s store.Mentions, q store.SharedMentionQuery) []domain.MessageKey {
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
	a1 := placed(t, s, roomA, 1, baseTime, targetMinh)
	b1 := placed(t, s, roomB, 1, baseTime.Add(time.Second), targetMinh, targetLan)
	placed(t, s, roomA, 2, baseTime.Add(2*time.Second), targetAll, targetOps)
	a3 := placed(t, s, roomA, 3, baseTime.Add(3*time.Second), targetMinh)
	gone := placed(t, s, roomA, 4, baseTime.Add(4*time.Second), targetMinh)
	retire(t, s, gone, baseTime.Add(4*time.Second), targetMinh)
	placedIn(t, s, otherTenant, roomB, 9, baseTime.Add(5*time.Second), targetMinh)
	q := store.TargetMentionQuery{Tenant: tenant, Target: targetMinh.Name(0), Limit: 10}
	if got, want := byTarget(t, s, q), []domain.MessageKey{a3, b1, a1}; !slices.Equal(got, want) {
		t.Fatalf("ListByTarget = %v, want %v", got, want)
	}
	q.Limit = 2
	if got, want := byTarget(t, s, q), []domain.MessageKey{a3, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListByTarget(limit 2) = %v, want %v", got, want)
	}
}

func listByTargetCursorTies(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, targetMinh)
	a2 := placed(t, s, roomA, 2, baseTime, targetMinh)
	a3 := placed(t, s, roomA, 3, baseTime, targetMinh)
	newer := placed(t, s, roomA, 4, baseTime.Add(time.Millisecond), targetMinh)
	q := store.TargetMentionQuery{Tenant: tenant, Target: targetMinh.Name(0), Limit: 3}
	if got, want := byTarget(t, s, q), []domain.MessageKey{newer, a3, a2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	q.Before = store.MentionCursor{At: baseTime, Key: a2}
	if got, want := byTarget(t, s, q), []domain.MessageKey{a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", a2, got, want)
	}
}

func listSharedSince(t *testing.T, s store.Mentions) {
	hanoi := domain.Target{Kind: domain.TargetGroup, ID: "hanoi"}
	placed(t, s, roomA, 1, baseTime, targetAll)
	a2 := placed(t, s, roomA, 2, baseTime.Add(2*time.Second), targetAll, targetMinh)
	a3 := placed(t, s, roomA, 3, baseTime.Add(3*time.Second), targetOps)
	placed(t, s, roomA, 4, baseTime.Add(4*time.Second), hanoi, targetMinh)
	b1 := placed(t, s, roomB, 1, baseTime.Add(time.Second), targetOps, targetAll)
	placed(t, s, roomC, 1, baseTime.Add(5*time.Second), targetAll)
	gone := placed(t, s, roomB, 2, baseTime.Add(6*time.Second), targetAll)
	retire(t, s, gone, baseTime.Add(6*time.Second), targetAll)
	placedIn(t, s, otherTenant, roomB, 3, baseTime.Add(7*time.Second), targetAll)
	rooms := []store.SharedRoom{{Room: roomA, Since: baseTime}, {Room: roomB}}
	q := store.SharedMentionQuery{Tenant: tenant, Rooms: rooms, Groups: []string{targetOps.ID}, Limit: 10}
	if got, want := shared(t, s, q), []domain.MessageKey{a3, a2, b1, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListShared = %v, want %v", got, want)
	}
	q.GroupSince = baseTime.Add(3 * time.Second)
	if got, want := shared(t, s, q), []domain.MessageKey{a3, a2, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListShared(group since 3s) = %v, want %v", got, want)
	}
	q.GroupSince = time.Time{}
	q.Groups = nil
	if got, want := shared(t, s, q), []domain.MessageKey{a2, b1}; !slices.Equal(got, want) {
		t.Fatalf("ListShared(no groups) = %v, want %v", got, want)
	}
	q.Rooms = []store.SharedRoom{{Room: roomA, Since: baseTime.Add(2 * time.Second)}}
	if got := shared(t, s, q); len(got) != 0 {
		t.Fatalf("ListShared(since the last @all) = %v, want nothing", got)
	}
}

func listSharedCursor(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, targetAll)
	b1 := placed(t, s, roomB, 1, baseTime, targetAll)
	a2 := placed(t, s, roomA, 2, baseTime.Add(time.Second), targetAll)
	c1 := placed(t, s, roomC, 1, baseTime.Add(2*time.Second), targetOps)
	q := store.SharedMentionQuery{Tenant: tenant, Rooms: []store.SharedRoom{{Room: roomA}, {Room: roomB}, {Room: roomC}}, Groups: []string{targetOps.ID}, Limit: 2}
	if got, want := shared(t, s, q), []domain.MessageKey{c1, a2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	q.Before = store.MentionCursor{At: baseTime.Add(time.Second), Key: a2}
	if got, want := shared(t, s, q), []domain.MessageKey{b1, a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", a2, got, want)
	}
	q.Before = store.MentionCursor{At: baseTime, Key: b1}
	if got, want := shared(t, s, q), []domain.MessageKey{a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", b1, got, want)
	}
}

func listInvalid(t *testing.T, s store.Mentions) {
	_, err := s.ListByTarget(t.Context(), store.TargetMentionQuery{Tenant: tenant, Limit: 10})
	assertInvalid(t, "ListByTarget(no target)", err)
	_, err = s.ListByTarget(t.Context(), store.TargetMentionQuery{Tenant: tenant, Target: targetMinh.Name(0), Limit: store.MaxPageLimit + 1})
	assertInvalid(t, "ListByTarget(limit over page)", err)
	_, err = s.ListShared(t.Context(), store.SharedMentionQuery{Tenant: tenant, Limit: 10})
	assertInvalid(t, "ListShared(no rooms)", err)
	_, err = s.ListShared(t.Context(), store.SharedMentionQuery{Tenant: "", Rooms: []store.SharedRoom{{Room: roomA}}, Limit: 10})
	assertInvalid(t, "ListShared(no tenant)", err)
}
