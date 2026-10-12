package storetest

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func factCases() []editCase {
	return []editCase{
		{"append then read each fact back by version", factsAppendAt},
		{"append of an existing version fails and keeps the first fact", factsAppendExisting},
		{"latest is the highest version of that message only", factsLatest},
		{"history ascends after a version up to the limit", factsHistory},
		{"between returns the room facts in a time range by time then key", factsBetween},
		{"purge text clears text and mentions up to a version of that message only", factsPurge},
		{"invalid facts and limits are rejected", factsInvalid},
	}
}

func factsAppendAt(t *testing.T, s editStores) {
	first, second := fact(roomA, mainThread, 1, 1), deletion(roomA, mainThread, 1, 2)
	mustAppend(t, s.edits, first, second)
	assertEditAt(t, s.edits, first)
	assertEditAt(t, s.edits, second)
	_, err := s.edits.At(t.Context(), store.EditKeyOf(first), 3)
	assertErrorIs(t, "At(missing version)", err, store.ErrEditNotFound)
	_, err = s.edits.At(t.Context(), msgKey(roomA, mainThread, 2), 1)
	assertErrorIs(t, "At(other message)", err, apperr.ErrNotFound)
}

func factsAppendExisting(t *testing.T, s editStores) {
	first := fact(roomA, mainThread, 1, 1)
	mustAppend(t, s.edits, first)
	rival := first
	rival.By, rival.Text = "bob", "rival"
	err := s.edits.Append(t.Context(), rival)
	assertErrorIs(t, "Append(existing version)", err, store.ErrEditExists)
	assertErrorIs(t, "Append(existing version)", err, apperr.ErrAlreadyExists)
	assertEditAt(t, s.edits, first)
}

func factsLatest(t *testing.T, s editStores) {
	key := msgKey(roomA, mainThread, 2)
	if _, ok, err := s.edits.Latest(t.Context(), key); ok || err != nil {
		t.Fatalf("Latest(no facts) = %v, %v; want false, nil", ok, err)
	}
	mustAppend(t, s.edits,
		fact(roomA, mainThread, 2, 1), fact(roomA, mainThread, 2, 3), fact(roomA, mainThread, 2, 2),
		fact(roomA, mainThread, 1, 9), fact(roomA, mainThread, 3, 7), fact(roomA, sideThread, 2, 8), fact(roomB, mainThread, 2, 6),
	)
	got, ok, err := s.edits.Latest(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v; want a fact", ok, err)
	}
	assertEdits(t, "Latest", []domain.Edit{got}, []domain.Edit{fact(roomA, mainThread, 2, 3)})
}

func factsHistory(t *testing.T, s editStores) {
	all := make([]domain.Edit, 0, 5)
	for v := uint32(1); v <= 5; v++ {
		all = append(all, fact(roomA, mainThread, 1, v))
	}
	mustAppend(t, s.edits, all...)
	mustAppend(t, s.edits, fact(roomA, mainThread, 2, 1))
	cases := []struct {
		after uint32
		limit int
		want  []domain.Edit
	}{
		{0, store.MaxEditPage, all},
		{2, 2, all[2:4]},
		{4, 10, all[4:]},
		{5, 10, nil},
		{math.MaxUint32, 1, nil},
	}
	for _, c := range cases {
		got, err := s.edits.History(t.Context(), msgKey(roomA, mainThread, 1), c.after, c.limit)
		if err != nil {
			t.Fatalf("History(after %d, limit %d): %v", c.after, c.limit, err)
		}
		assertEdits(t, fmt.Sprintf("History(after %d, limit %d)", c.after, c.limit), got, c.want)
	}
}

func factsBetween(t *testing.T, s editStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	atFrom := fact(roomA, mainThread, 5, 1)
	before := fact(roomA, mainThread, 4, 1)
	before.At = baseTime
	lowKey, highKey := fact(roomA, mainThread, 2, 2), fact(roomA, mainThread, 3, 2)
	atTo, after, other := fact(roomA, sideThread, 1, 3), fact(roomA, mainThread, 1, 4), fact(roomB, mainThread, 1, 2)
	mustAppend(t, s.edits, after, highKey, other, atTo, before, lowKey, atFrom)
	want := []domain.Edit{atFrom, lowKey, highKey, atTo}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.Edit
	}{
		{roomA, from, to, store.MaxEditScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.Edit{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.edits.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, %v, %v, %d): %v", c.room, c.from, c.to, c.limit, err)
		}
		assertEdits(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func factsPurge(t *testing.T, s editStores) {
	v1, v2, v3 := mentioning(fact(roomA, mainThread, 1, 1)), fact(roomA, mainThread, 1, 2), deletion(roomA, mainThread, 1, 3)
	neighbour := mentioning(fact(roomA, mainThread, 2, 1))
	mustAppend(t, s.edits, v1, v2, v3, neighbour)
	key := store.EditKeyOf(v3)
	if err := s.edits.PurgeText(t.Context(), key, 2); err != nil {
		t.Fatalf("PurgeText(up to 2): %v", err)
	}
	if err := s.edits.PurgeText(t.Context(), key, 0); err != nil {
		t.Fatalf("PurgeText(up to 0): %v", err)
	}
	v1.Text, v2.Text, v1.Mentions, v1.MentionAll = "", "", nil, false
	for _, want := range []domain.Edit{v1, v2, v3, neighbour} {
		assertEditAt(t, s.edits, want)
	}
}

func factsInvalid(t *testing.T, s editStores) {
	for name, mutate := range map[string]func(*domain.Edit){
		"zero room":               func(e *domain.Edit) { e.Room = 0 },
		"zero seq":                func(e *domain.Edit) { e.Seq = 0 },
		"zero version":            func(e *domain.Edit) { e.Version = 0 },
		"version above max int32": func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 },
		"zero kind":               func(e *domain.Edit) { e.Kind = 0 },
		"original kind above 0":   func(e *domain.Edit) { e.Kind = domain.EditOriginal },
		"duplicate mention":       func(e *domain.Edit) { e.Mentions = []domain.MentionTarget{mentionMinh, mentionMinh} },
		"@all as a target":        func(e *domain.Edit) { e.Mentions = []domain.MentionTarget{mentionAll} },
		"delete with mentions":    func(e *domain.Edit) { *e = mentioning(deletion(roomA, mainThread, 1, 1)) },
	} {
		e := fact(roomA, mainThread, 1, 1)
		mutate(&e)
		assertErrorIs(t, "Append("+name+")", s.edits.Append(t.Context(), e), apperr.ErrInvalidArgument)
		assertErrorIs(t, "ApplyEdit("+name+")", s.msgs.ApplyEdit(t.Context(), e), apperr.ErrInvalidArgument)
	}
	key := msgKey(roomA, mainThread, 1)
	for _, limit := range []int{-1, 0, store.MaxEditPage + 1} {
		_, err := s.edits.History(t.Context(), key, 0, limit)
		assertErrorIs(t, fmt.Sprintf("History(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	for _, limit := range []int{0, store.MaxEditScan + 1} {
		_, err := s.edits.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, fmt.Sprintf("Between(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	if _, ok, err := s.edits.Latest(t.Context(), key); ok || err != nil {
		t.Fatalf("Latest after invalid appends = %v, %v; want nothing stored", ok, err)
	}
}
