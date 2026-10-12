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

func pinFactCases() []pinCase {
	return []pinCase{
		{"append then read each fact back by version", pinsAppendAt},
		{"append of an existing version fails and keeps the first fact", pinsAppendExisting},
		{"after ascends past a version up to the limit in that room only", pinsAfter},
		{"between returns the room facts in a time range by time then version", pinsBetween},
		{"invalid facts and limits are rejected", pinsInvalid},
	}
}

func pinsAppendAt(t *testing.T, s pinStores) {
	first, second := pinFact(roomA, 1, domain.PinOpPin, 3), pinFact(roomA, 2, domain.PinOpUnpin, 3)
	second.Thread = sideThread
	mustAppendPins(t, s.pins, first, second)
	assertPinAt(t, s.pins, first)
	assertPinAt(t, s.pins, second)
	_, err := s.pins.At(t.Context(), roomA, 3)
	assertErrorIs(t, "At(missing version)", err, store.ErrPinNotFound)
	_, err = s.pins.At(t.Context(), roomB, 1)
	assertErrorIs(t, "At(other room)", err, apperr.ErrNotFound)
}

func pinsAppendExisting(t *testing.T, s pinStores) {
	first := pinFact(roomA, 1, domain.PinOpPin, 3)
	mustAppendPins(t, s.pins, first)
	rival := first
	rival.By, rival.Seq = "bob", 9
	err := s.pins.Append(t.Context(), rival)
	assertErrorIs(t, "Append(existing version)", err, store.ErrPinExists)
	assertErrorIs(t, "Append(existing version)", err, apperr.ErrAlreadyExists)
	assertPinAt(t, s.pins, first)
}

func pinsAfter(t *testing.T, s pinStores) {
	all := make([]domain.PinAction, 0, 5)
	for pv := uint32(1); pv <= 5; pv++ {
		all = append(all, pinFact(roomA, pv, domain.PinOpPin, uint64(pv)))
	}
	other := pinFact(roomB, 1, domain.PinOpPin, 1)
	mustAppendPins(t, s.pins, all...)
	mustAppendPins(t, s.pins, other)
	cases := []struct {
		room, after uint64
		limit       int
		want        []domain.PinAction
	}{
		{roomA, 0, store.MaxPinScan, all},
		{roomA, 2, 2, all[2:4]},
		{roomA, 5, 10, nil},
		{roomA, math.MaxUint64, 1, nil},
		{roomB, 0, 10, []domain.PinAction{other}},
	}
	for _, c := range cases {
		got, err := s.pins.After(t.Context(), c.room, c.after, c.limit)
		if err != nil {
			t.Fatalf("After(%d, %d, %d): %v", c.room, c.after, c.limit, err)
		}
		assertPinActions(t, fmt.Sprintf("After(%d, %d, %d)", c.room, c.after, c.limit), got, c.want)
	}
}

func pinsBetween(t *testing.T, s pinStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	atFrom, mid, atTo, after := pinFact(roomA, 1, domain.PinOpPin, 1), pinFact(roomA, 2, domain.PinOpPin, 2), pinFact(roomA, 3, domain.PinOpPin, 3), pinFact(roomA, 4, domain.PinOpPin, 4)
	sameTime, before, other := pinFact(roomA, 5, domain.PinOpUnpin, 2), pinFact(roomA, 6, domain.PinOpPin, 6), pinFact(roomB, 1, domain.PinOpPin, 1)
	sameTime.At, before.At, other.At = mid.At, baseTime, mid.At
	mustAppendPins(t, s.pins, after, other, atTo, before, sameTime, mid, atFrom)
	want := []domain.PinAction{atFrom, mid, sameTime, atTo}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.PinAction
	}{
		{roomA, from, to, store.MaxPinScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.PinAction{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.pins.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, limit %d): %v", c.room, c.limit, err)
		}
		assertPinActions(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func pinsInvalid(t *testing.T, s pinStores) {
	for name, mutate := range map[string]func(*domain.PinAction){
		"zero room":               func(a *domain.PinAction) { a.Room = 0 },
		"zero version":            func(a *domain.PinAction) { a.PV = 0 },
		"version above max int64": func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 },
		"zero op":                 func(a *domain.PinAction) { a.Op = 0 },
		"op past unpin":           func(a *domain.PinAction) { a.Op = domain.PinOpUnpin + 1 },
		"zero seq":                func(a *domain.PinAction) { a.Seq = 0 },
		"user with a dot":         func(a *domain.PinAction) { a.By = "a.b" },
	} {
		a := pinFact(roomA, 1, domain.PinOpPin, 3)
		mutate(&a)
		assertErrorIs(t, "Append("+name+")", s.pins.Append(t.Context(), a), apperr.ErrInvalidArgument)
	}
	for _, limit := range []int{0, store.MaxPinScan + 1} {
		_, err := s.pins.After(t.Context(), roomA, 0, limit)
		assertErrorIs(t, fmt.Sprintf("After(limit %d)", limit), err, apperr.ErrInvalidArgument)
		_, err = s.pins.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, fmt.Sprintf("Between(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	if got, err := s.pins.After(t.Context(), roomA, 0, store.MaxPinScan); err != nil || len(got) != 0 {
		t.Fatalf("After after invalid appends = %+v, %v; want nothing stored", got, err)
	}
}
