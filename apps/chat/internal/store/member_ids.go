package store

import (
	"context"
	"math"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

type MemberIDs interface {
	MemberUserIDs(ctx context.Context, room uint64) ([]string, error)
}

type AvatarMembers interface {
	MemberSample(ctx context.Context, room uint64, limit int) ([]string, error)
	SetAvatarUsers(ctx context.Context, room, ver uint64, next []string) error
}

func ValidateMemberSample(room uint64, limit int) error {
	if err := ValidateMemberIDsRoom(room); err != nil {
		return err
	}
	if limit < 1 || limit > domain.MaxAvatarUsers {
		return invalid("limit")
	}
	return nil
}

func ValidateAvatarUsers(room, ver uint64, next []string) error {
	if err := ValidateMemberIDsRoom(room); err != nil {
		return err
	}
	if ver > math.MaxInt64 {
		return invalid("avatar ver")
	}
	if len(next) > domain.MaxAvatarUsers {
		return invalid("avatar users")
	}
	return nil
}

func ValidateMemberIDsRoom(room uint64) error {
	if room == 0 || room > math.MaxInt64 {
		return invalid("room id")
	}
	return nil
}
