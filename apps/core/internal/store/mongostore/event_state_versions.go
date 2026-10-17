package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func versionMatch(t store.StateTarget) (bson.E, bool) {
	field, _ := versionField(t.Collection, t.Bit)
	switch {
	case field == clearedAtField && t.At.IsZero():
		return bson.E{Key: field, Value: nil}, true
	case field == clearedAtField:
		return bson.E{Key: field, Value: t.At.UTC().Truncate(time.Millisecond)}, true
	case t.Ver == 0:
		return bson.E{Key: field, Value: bson.D{{Key: "$in", Value: bson.A{int64(0), nil}}}}, true
	}
	v, ok := int64Of(t.Ver)
	return bson.E{Key: field, Value: v}, ok
}

func versionField(c store.StateCollection, bit store.StateBit) (string, bool) {
	switch {
	case c == store.StateMessages && bit == store.BitReactionCounts:
		return "rx.v", true
	case c == store.StateMessages && bit == store.BitReplyCounts:
		return "rc.v", true
	case c == store.StateInteractions:
		return "ver", true
	case c == store.StateMembers:
		return memberVersionField(bit)
	case c == store.StateRooms && bit == store.BitMemberCount:
		return "member_count_ver", true
	case c == store.StateRooms && bit == store.BitRoomInfo:
		return "info_ver", true
	default:
		return "", false
	}
}

func memberVersionField(bit store.StateBit) (string, bool) {
	switch bit {
	case store.BitMember:
		return "ver", true
	case store.BitReadPosition:
		return "read_ver", true
	case store.BitHistoryCleared:
		return clearedAtField, true
	case store.BitPreferences:
		return "preference_ver", true
	default:
		return "", false
	}
}
