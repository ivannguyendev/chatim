package store

import (
	"context"
	"math"
	"time"
)

const MaxMessageLookup = 200

type MessageTimes interface {
	SentTimes(ctx context.Context, keys []MsgKey) (map[MsgKey]time.Time, error)
}

type MessageVersions interface {
	MessageVersions(ctx context.Context, keys []MsgKey) (map[MsgKey]uint32, error)
}

func ValidateMessageLookup(keys []MsgKey) error {
	if len(keys) > MaxMessageLookup {
		return invalid("message keys")
	}
	for _, k := range keys {
		if k.Validate() != nil || k.Room > math.MaxInt64 {
			return invalid("message key")
		}
	}
	return nil
}
