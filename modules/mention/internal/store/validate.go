package store

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	MaxPageLimit       = 100
	MaxGroupMemberPage = 1000
	SharedMentionChunk = 1000
	SharedMentionBatch = 200
	MentionRounds      = 3
)

func ValidateLimit(limit, maxLimit int) error {
	if limit < 1 || limit > maxLimit {
		return invalid("limit")
	}
	return nil
}

func ValidateRoom(room uint64) error {
	if room == 0 || room > math.MaxInt64 {
		return invalid("room")
	}
	return nil
}

func ValidateRaise(tenant string, room uint64, at time.Time) error {
	if err := ids.ValidTenant(tenant); err != nil {
		return err
	}
	if at.IsZero() {
		return invalid("time")
	}
	return ValidateRoom(room)
}

func ValidateRooms(tenant string, rooms []uint64) error {
	if err := ids.ValidTenant(tenant); err != nil {
		return err
	}
	for _, r := range rooms {
		if err := ValidateRoom(r); err != nil {
			return err
		}
	}
	return nil
}

func ValidateMentionsOf(tenant string, key domain.MessageKey) error {
	if err := ids.ValidTenant(tenant); err != nil {
		return err
	}
	return key.Validate()
}

func ValidateUsers(tenant string, users []string) error {
	if err := ids.ValidTenant(tenant); err != nil {
		return err
	}
	for _, u := range users {
		if err := ids.ValidUser(u); err != nil {
			return err
		}
	}
	return nil
}

func ValidateGroupMembersQuery(tenant, group, after string, limit int) error {
	if err := ids.ValidTenant(tenant); err != nil {
		return err
	}
	if !keys.ValidMentionID(group) {
		return invalid("group id")
	}
	if after != "" {
		if err := ids.ValidUser(after); err != nil {
			return err
		}
	}
	return ValidateLimit(limit, MaxGroupMemberPage)
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
}

func ValidateTenant(tenant string) error { return ids.ValidTenant(tenant) }
