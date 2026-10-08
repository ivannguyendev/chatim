package ownership_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
)

const room uint64 = 42

var base = time.UnixMilli(1_700_000_000_000).UTC()

func secs(n int) time.Time { return base.Add(time.Duration(n) * time.Second) }

func seat(user string, role domain.Role, joinedSec int, priority int32) domain.Member {
	return domain.Member{
		Room: room, Tenant: "acme", User: user, Role: role, JoinedAt: secs(joinedSec), State: domain.MemberActive,
		Ver: 1, Priority: priority, RequestID: domain.CreationRequestID(room), UpdatedAt: base, UpdatedBy: "alice",
		LastChangeAt: base,
	}
}

func assertSuccessor(t *testing.T, candidates []domain.Member, want string) {
	t.Helper()
	got, ok := ownership.Successor(candidates)
	if !ok || got.User != want {
		t.Fatalf("Successor = %q, %v; want %q", got.User, ok, want)
	}
}

func TestSuccessorPrefersAnAdmin(t *testing.T) {
	assertSuccessor(t, []domain.Member{
		seat("aaron", domain.RoleMember, 0, 99),
		seat("zed", domain.RoleAdmin, 9, -5),
	}, "zed")
}

func TestAmongOneRoleTheHighestPriorityWins(t *testing.T) {
	assertSuccessor(t, []domain.Member{
		seat("anna", domain.RoleAdmin, 0, 1),
		seat("zack", domain.RoleAdmin, 9, 7),
		seat("bert", domain.RoleAdmin, 1, -3),
	}, "zack")
	assertSuccessor(t, []domain.Member{
		seat("anna", domain.RoleMember, 0, -1),
		seat("zack", domain.RoleMember, 9, 0),
	}, "zack")
}

func TestEqualPrioritiesGoToTheEarliestJoinThenTheSmallestUserID(t *testing.T) {
	assertSuccessor(t, []domain.Member{
		seat("anna", domain.RoleAdmin, 5, 2),
		seat("zack", domain.RoleAdmin, 1, 2),
	}, "zack")
	assertSuccessor(t, []domain.Member{
		seat("carl", domain.RoleMember, 3, 0),
		seat("bert", domain.RoleMember, 3, 0),
		seat("dina", domain.RoleMember, 4, 0),
	}, "bert")
}

func TestNoCandidateMeansNoSuccessor(t *testing.T) {
	if got, ok := ownership.Successor(nil); ok {
		t.Fatalf("Successor(nil) = %+v, true; want none", got)
	}
}
