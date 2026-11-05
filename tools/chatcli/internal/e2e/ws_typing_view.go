package e2e

import (
	"cmp"
	"slices"
	"time"
)

const TypingTimeout = 6 * time.Second

type Typist struct {
	Room string
	User string
}

type TypingView struct {
	shown map[Typist]time.Time
}

func (v *TypingView) Start(who Typist, now time.Time) {
	if v.shown == nil {
		v.shown = map[Typist]time.Time{}
	}
	v.shown[who] = now.Add(TypingTimeout)
}

func (v *TypingView) End(who Typist) bool {
	if _, ok := v.shown[who]; !ok {
		return false
	}
	delete(v.shown, who)
	return true
}

func (v *TypingView) Next() (time.Time, bool) {
	var next time.Time
	for _, deadline := range v.shown {
		if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	return next, !next.IsZero()
}

func (v *TypingView) Expire(now time.Time) []Typist {
	var ended []Typist
	for who, deadline := range v.shown {
		if !deadline.After(now) {
			ended = append(ended, who)
			delete(v.shown, who)
		}
	}
	slices.SortFunc(ended, func(a, b Typist) int {
		return cmp.Or(cmp.Compare(a.Room, b.Room), cmp.Compare(a.User, b.User))
	})
	return ended
}
