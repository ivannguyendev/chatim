package ownership

import (
	"cmp"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func Successor(candidates []domain.Member) (domain.Member, bool) {
	if len(candidates) == 0 {
		return domain.Member{}, false
	}
	return slices.MinFunc(candidates, bySuccession), true
}

func bySuccession(a, b domain.Member) int {
	return cmp.Or(
		cmp.Compare(roleRank(a.Role), roleRank(b.Role)),
		cmp.Compare(b.Priority, a.Priority),
		a.JoinedAt.Compare(b.JoinedAt),
		cmp.Compare(a.User, b.User),
	)
}

func roleRank(r domain.Role) int {
	if r == domain.RoleAdmin {
		return 0
	}
	return 1
}
