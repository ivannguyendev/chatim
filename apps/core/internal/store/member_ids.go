package store

import (
	"context"
	"math"
)

type MemberIDs interface {
	MemberUserIDs(ctx context.Context, room uint64) ([]string, error)
}

func ValidateMemberIDsRoom(room uint64) error {
	if room == 0 || room > math.MaxInt64 {
		return invalid("room id")
	}
	return nil
}
