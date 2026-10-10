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
		{"ListGivesLiveWantedTargetsNewestFirst", listNewestFirst},
		{"ListSinceHidesOlderMentionsOfThatTargetOnly", listSince},
		{"ListContinuesAfterTheCursorAcrossTies", listCursorTies},
		{"ListRejectsABadQuery", listInvalid},
	}
}

func placed(t *testing.T, s store.Mentions, room, seq uint64, at time.Time, targets ...domain.MentionTarget) store.MsgKey {
	t.Helper()
	set := store.MentionSet{Key: store.MsgKey{Room: room, Seq: seq}, Tenant: tenant, Sender: "alice", Targets: targets, CreatedAt: at, At: at}
	mustApplyMentions(t, s, set)
	return set.Key
}

func wants(room uint64, targets ...domain.MentionTarget) []store.MentionWant {
	out := make([]store.MentionWant, len(targets))
	for i, tg := range targets {
		out[i] = store.MentionWant{Target: tg.Name(room)}
	}
	return out
}

func listKeys(t *testing.T, s store.Mentions, q store.MentionQuery) []store.MsgKey {
	t.Helper()
	got, err := s.List(t.Context(), q)
	if err != nil {
		t.Fatalf("List(%+v): %v", q, err)
	}
	out := make([]store.MsgKey, len(got))
	for i, m := range got {
		if !m.Live || m.Tenant != q.Tenant || !q.Wants(m) {
			t.Fatalf("List returned %+v, outside the query", m)
		}
		out[i] = store.MsgKey(m.Key)
	}
	return out
}

func listNewestFirst(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionMinh)
	a2 := placed(t, s, roomA, 2, baseTime.Add(2*time.Second), mentionOps, mentionAll)
	b1 := placed(t, s, roomB, 1, baseTime.Add(time.Second), mentionMinh, mentionLan)
	placed(t, s, roomA, 3, baseTime.Add(3*time.Second), mentionLan)
	placed(t, s, roomB, 4, baseTime.Add(4*time.Second), mentionAll)
	gone := placed(t, s, roomA, 4, baseTime.Add(4*time.Second), mentionMinh)
	mustApplyMentions(t, s, store.MentionSet{Key: gone, Tenant: tenant, Sender: "alice", Ver: 1, CreatedAt: baseTime.Add(4 * time.Second), At: baseTime.Add(time.Hour)})
	other := store.MentionSet{Key: store.MsgKey{Room: roomB, Seq: 9}, Tenant: "globex", Sender: "alice", Targets: []domain.MentionTarget{mentionMinh}, CreatedAt: baseTime.Add(5 * time.Second), At: baseTime}
	mustApplyMentions(t, s, other)
	q := store.MentionQuery{Tenant: tenant, Targets: wants(roomA, mentionMinh, mentionAll, mentionOps), Limit: 10}
	if got, want := listKeys(t, s, q), []store.MsgKey{a2, a2, b1, a1}; !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	q.Limit = 2
	if got, want := listKeys(t, s, q), []store.MsgKey{a2, a2}; !slices.Equal(got, want) {
		t.Fatalf("List(limit 2) = %v, want %v", got, want)
	}
}

func listSince(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionOps)
	a2 := placed(t, s, roomA, 2, baseTime.Add(2*time.Second), mentionOps, mentionMinh)
	a3 := placed(t, s, roomA, 3, baseTime.Add(3*time.Second), mentionOps)
	ops := store.MentionWant{Target: mentionOps.Name(roomA), Since: baseTime.Add(3 * time.Second)}
	q := store.MentionQuery{Tenant: tenant, Targets: []store.MentionWant{ops}, Limit: 10}
	if got, want := listKeys(t, s, q), []store.MsgKey{a3}; !slices.Equal(got, want) {
		t.Fatalf("List(ops since 3s) = %v, want %v", got, want)
	}
	q.Targets = append(wants(roomA, mentionMinh), ops)
	if got, want := listKeys(t, s, q), []store.MsgKey{a3, a2}; !slices.Equal(got, want) {
		t.Fatalf("List(minh, ops since 3s) = %v, want %v", got, want)
	}
	ops.Since = baseTime
	q.Targets = []store.MentionWant{ops}
	if got, want := listKeys(t, s, q), []store.MsgKey{a3, a2, a1}; !slices.Equal(got, want) {
		t.Fatalf("List(ops since the first) = %v, want %v", got, want)
	}
}

func listCursorTies(t *testing.T, s store.Mentions) {
	a1 := placed(t, s, roomA, 1, baseTime, mentionMinh)
	a2 := placed(t, s, roomA, 2, baseTime, mentionMinh, mentionAll)
	a3 := placed(t, s, roomA, 3, baseTime, mentionMinh)
	newer := placed(t, s, roomA, 4, baseTime.Add(time.Millisecond), mentionMinh)
	q := store.MentionQuery{Tenant: tenant, Targets: wants(roomA, mentionMinh, mentionAll), Limit: 3}
	if got, want := listKeys(t, s, q), []store.MsgKey{newer, a3, a2}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	q.Before = store.MentionCursor{At: baseTime, Key: a2}
	if got, want := listKeys(t, s, q), []store.MsgKey{a1}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", a2, got, want)
	}
	q.Before = store.MentionCursor{At: baseTime.Add(time.Millisecond), Key: newer}
	if got, want := listKeys(t, s, q), []store.MsgKey{a3, a2, a2}; !slices.Equal(got, want) {
		t.Fatalf("after %v = %v, want %v", newer, got, want)
	}
}

func listInvalid(t *testing.T, s store.Mentions) {
	good := store.MentionQuery{Tenant: tenant, Targets: wants(roomA, mentionMinh), Limit: 10}
	for name, edit := range map[string]func(*store.MentionQuery){
		"no tenant":        func(q *store.MentionQuery) { q.Tenant = "" },
		"no targets":       func(q *store.MentionQuery) { q.Targets = nil },
		"empty target":     func(q *store.MentionQuery) { q.Targets = []store.MentionWant{{}} },
		"duplicate target": func(q *store.MentionQuery) { q.Targets = wants(roomA, mentionMinh, mentionMinh) },
		"zero limit":       func(q *store.MentionQuery) { q.Limit = 0 },
		"limit over page":  func(q *store.MentionQuery) { q.Limit = store.MaxPageLimit + 1 },
		"cursor without a key": func(q *store.MentionQuery) {
			q.Before = store.MentionCursor{At: baseTime}
		},
	} {
		q := good
		edit(&q)
		if _, err := s.List(t.Context(), q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: List = %v, want ErrInvalidArgument", name, err)
		}
	}
}
