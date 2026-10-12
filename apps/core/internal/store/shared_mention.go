package store

import (
	"context"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const SharedMentionChunk = 1000

type SharedMentions interface {
	RaiseSharedMention(ctx context.Context, room uint64, at time.Time) error
	SharedMentionTimes(ctx context.Context, rooms []uint64) (map[uint64]time.Time, error)
}

type MessageTimes interface {
	SentTimes(ctx context.Context, keys []MsgKey) (map[MsgKey]time.Time, error)
}

func ValidateSentTimeKeys(keys []MsgKey) error {
	if len(keys) > SharedMentionBatch {
		return invalid("message keys")
	}
	for _, k := range keys {
		if k.Validate() != nil || k.Room > math.MaxInt64 {
			return invalid("message key")
		}
	}
	return nil
}

func ValidateSharedMention(room uint64, at time.Time) error {
	switch {
	case room == 0 || room > math.MaxInt64:
		return invalid("room")
	case at.IsZero():
		return invalid("time")
	}
	return nil
}

func ValidateSharedMentionRooms(rooms []uint64) error {
	for _, r := range rooms {
		if r == 0 || r > math.MaxInt64 {
			return invalid("room")
		}
	}
	return nil
}

func ValidateMentionRead(room uint64, user string, readSeq uint64) error {
	switch {
	case room == 0 || room > math.MaxInt64:
		return invalid("room")
	case domain.ValidUser(user) != nil:
		return invalid("user")
	}
	return ValidateReadSeq(readSeq)
}
