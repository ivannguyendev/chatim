package store

import (
	"cmp"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type Reactor struct {
	User  string
	Emoji string
	At    time.Time
}

type ReactorCursor struct {
	Emoji string
	At    time.Time
	User  string
}

type ReactorQuery struct {
	Key   MsgKey
	Emoji string
	After ReactorCursor
	Limit int
}

func ValidateReactorQuery(q ReactorQuery) error {
	if err := q.Key.Validate(); err != nil {
		return err
	}
	if q.Emoji != "" {
		if err := domain.ValidateEmoji(q.Emoji); err != nil {
			return err
		}
	}
	if err := validReactorCursor(q.After, q.Emoji == ""); err != nil {
		return err
	}
	return ValidateLimit(q.Limit, MaxPageLimit)
}

func validReactorCursor(c ReactorCursor, byEmoji bool) error {
	switch {
	case c.At.IsZero() != (c.User == ""):
		return invalid("cursor")
	case c.User == "":
		return nil
	case byEmoji:
		if err := domain.ValidateEmoji(c.Emoji); err != nil {
			return err
		}
	}
	return domain.ValidUser(c.User)
}

func (q ReactorQuery) Cursor(r Reactor) ReactorCursor {
	c := ReactorCursor{At: r.At, User: r.User}
	if q.Emoji == "" {
		c.Emoji = r.Emoji
	}
	return c
}

func (q ReactorQuery) Order(a, b Reactor) int {
	byTime := cmp.Or(b.At.Compare(a.At), cmp.Compare(a.User, b.User))
	if q.Emoji != "" {
		return byTime
	}
	return cmp.Or(cmp.Compare(a.Emoji, b.Emoji), byTime)
}

func (q ReactorQuery) Wants(r Reactor) bool {
	if q.Emoji != "" && r.Emoji != q.Emoji {
		return false
	}
	if q.After.User == "" {
		return true
	}
	return q.Order(q.reactorAt(), r) < 0
}

func (q ReactorQuery) reactorAt() Reactor {
	return Reactor{User: q.After.User, Emoji: q.After.Emoji, At: q.After.At}
}
