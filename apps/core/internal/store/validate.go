package store

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxPageLimit = 100

var ErrRoomExists = fmt.Errorf("room %w", apperr.ErrAlreadyExists)

func KeyOf(m domain.Message) MsgKey {
	return MsgKey{Room: m.Room, Thread: m.Thread, Seq: m.Seq}
}

func (k MsgKey) Validate() error {
	switch {
	case k.Room == 0:
		return invalid("room")
	case k.Seq == 0:
		return invalid("seq")
	default:
		return nil
	}
}

func ValidateKeys(room uint64, keys []MsgKey) error {
	for _, k := range keys {
		if k.Room != room {
			return invalid("key room")
		}
	}
	return nil
}

func ValidateRoom(r domain.Room, members []domain.Member) error {
	switch {
	case r.ID == 0:
		return invalid("room id")
	case r.Tenant == "":
		return invalid("tenant")
	case len(members) == 0:
		return invalid("members")
	}
	for _, m := range members {
		if m.Room != r.ID || m.Tenant != r.Tenant {
			return invalid("member")
		}
	}
	return nil
}

func (q PageQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxPageLimit {
		return invalid("limit")
	}
	switch q.Anchor {
	case Latest, Oldest, Before, After:
		return nil
	default:
		return invalid("anchor")
	}
}

func ValidateVersionBump(base, next uint64) error {
	if next <= base || next > math.MaxInt64 {
		return invalid("version")
	}
	return nil
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
}
