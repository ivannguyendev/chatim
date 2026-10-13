package store

import (
	"cmp"
	"context"
	"math"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const MaxTailPreviews = 1000

type RoomTails interface {
	TailWindow(ctx context.Context, room, afterSeq uint64, limit int) ([]domain.TailMessage, error)
	TailPreviews(ctx context.Context, keys []MsgKey) ([]domain.Message, error)
}

type HiddenRange struct {
	Room, From, To uint64
}

func ValidateTailWindow(room, afterSeq uint64, limit int) error {
	switch {
	case room == 0 || room > math.MaxInt64:
		return invalid("room")
	case afterSeq > math.MaxInt64:
		return invalid("after seq")
	}
	return ValidateLimit(limit, domain.UnreadWindow)
}

func ValidateTailPreviews(keys []MsgKey) error {
	if len(keys) > MaxTailPreviews {
		return invalid("message keys")
	}
	for _, k := range keys {
		if k.Validate() != nil || k.Room > math.MaxInt64 || k.Seq > math.MaxInt64 {
			return invalid("message key")
		}
	}
	return nil
}

func SortByKey(msgs []domain.Message) {
	slices.SortFunc(msgs, func(a, b domain.Message) int {
		return cmp.Or(cmp.Compare(a.Room, b.Room), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq))
	})
}

func ValidateHiddenRanges(user string, ranges []HiddenRange) error {
	if len(ranges) > MaxHiddenScan {
		return invalid("hidden ranges")
	}
	for _, r := range ranges {
		if r.Room == 0 || r.Room > math.MaxInt64 || r.From > r.To || r.To > math.MaxInt64 {
			return invalid("hidden range")
		}
	}
	return domain.ValidUser(user)
}
