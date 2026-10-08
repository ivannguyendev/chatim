package store

import (
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

const maxOwnerWrites = 2

func ValidateJoin(j domain.Join, users []string) error {
	switch {
	case j.Room == 0:
		return invalid("room")
	case domain.ValidTenant(j.Tenant) != nil:
		return invalid("tenant")
	case domain.ValidCID(j.RequestID) != nil:
		return invalid("request id")
	case domain.ValidUser(j.By) != nil:
		return invalid("caller")
	case j.At.IsZero():
		return invalid("time")
	case ValidateReadSeq(j.ReadSeq) != nil:
		return invalid("read seq")
	case len(users) < 1 || len(users) > domain.MaxMemberBatch:
		return invalid("users")
	}
	seen := make(map[string]struct{}, len(users))
	for _, u := range users {
		if domain.ValidUser(u) != nil {
			return invalid("user")
		}
		if _, dup := seen[u]; dup {
			return invalid("duplicate user")
		}
		seen[u] = struct{}{}
	}
	return nil
}

func ValidateMemberChange(cur, next domain.Member) error {
	switch {
	case cur.Room != next.Room || cur.User != next.User:
		return invalid("member")
	case cur.Ver < 1 || uint64(next.Ver) != uint64(cur.Ver)+1:
		return invalid("member ver")
	case next.State != domain.MemberActive && next.State != domain.MemberRemoved:
		return invalid("member state")
	}
	if _, err := domain.ParseRole(string(next.Role)); err != nil {
		return err
	}
	if domain.ValidCID(next.RequestID) != nil {
		return invalid("request id")
	}
	if domain.ValidUser(next.UpdatedBy) != nil {
		return invalid("updated by")
	}
	return nil
}

func ValidateOwnerWrites(room uint64, writes []MemberWrite) error {
	if len(writes) < 1 || len(writes) > maxOwnerWrites {
		return invalid("owner writes")
	}
	seen := make(map[string]struct{}, len(writes))
	for _, w := range writes {
		if w.Cur.Room != room {
			return invalid("owner write room")
		}
		if _, dup := seen[w.Cur.User]; dup {
			return invalid("owner write user")
		}
		seen[w.Cur.User] = struct{}{}
		if err := ValidateMemberChange(w.Cur, w.Next); err != nil {
			return err
		}
	}
	return nil
}

func ValidateReadSeq(seq uint64) error {
	if seq > math.MaxInt64 {
		return invalid("read seq")
	}
	return nil
}

func ValidateMemberDelta(delta int) error {
	if delta == 0 {
		return invalid("member count delta")
	}
	return nil
}

func ValidateMemberCount(count int) error {
	if count < 0 {
		return invalid("member count")
	}
	return nil
}

func CreationMember(r domain.Room, m domain.Member) domain.Member {
	m.State, m.Ver, m.RequestID = domain.MemberActive, 1, domain.CreationRequestID(r.ID)
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = r.CreatedAt
	}
	if m.UpdatedBy == "" {
		m.UpdatedBy = r.CreatedBy
	}
	if m.LastChangeAt.IsZero() {
		m.LastChangeAt = r.CreatedAt
	}
	return m
}
