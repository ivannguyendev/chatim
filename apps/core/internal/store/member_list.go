package store

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const MaxMemberPage = 1000

var MemberListRoles = []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember}

type MemberCursor struct {
	Role     domain.Role
	Priority int32
	JoinedAt time.Time
	User     string
}

func ValidateMemberList(room uint64, after MemberCursor, limit int) error {
	if room == 0 || room > math.MaxInt64 {
		return invalid("room id")
	}
	if after != (MemberCursor{}) {
		if !slices.Contains(MemberListRoles, after.Role) || after.JoinedAt.IsZero() {
			return invalid("cursor")
		}
		if err := domain.ValidUser(after.User); err != nil {
			return err
		}
	}
	return ValidateLimit(limit, MaxMemberPage)
}

func MemberCursorOf(m domain.Member) MemberCursor {
	return MemberCursor{Role: m.Role, Priority: m.Priority, JoinedAt: m.JoinedAt, User: m.User}
}

func RolesFrom(after MemberCursor) []domain.Role {
	if after == (MemberCursor{}) {
		return MemberListRoles
	}
	return MemberListRoles[slices.Index(MemberListRoles, after.Role):]
}

func CompareMemberOrder(a, b MemberCursor) int {
	return cmp.Or(
		cmp.Compare(slices.Index(MemberListRoles, a.Role), slices.Index(MemberListRoles, b.Role)),
		cmp.Compare(b.Priority, a.Priority),
		a.JoinedAt.Compare(b.JoinedAt),
		cmp.Compare(a.User, b.User),
	)
}

func (c MemberCursor) Before(m domain.Member) bool {
	return c == (MemberCursor{}) || CompareMemberOrder(c, MemberCursorOf(m)) < 0
}
