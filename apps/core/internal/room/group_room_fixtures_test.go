package room_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const (
	group  uint64 = 7070
	direct uint64 = 7171
)

func newMemberRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := newRig(t, policy)
	r, members, err := domain.NewRoom(tenant, "owen", domain.RoomGroup, "crew", []string{"owen", "ada", "mia", "max"}, created, group)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create group: %v", err)
	}
	rg.seedRole(t, "ada", domain.RoleAdmin)
	return rg
}

func (rg *rig) seedRole(t *testing.T, user string, role domain.Role) domain.Member {
	t.Helper()
	cur := rg.doc(t, user)
	next := cur.Next(role, domain.MemberActive, cur.Priority, "seed-"+user, "owen", created)
	if ok, err := rg.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("seed %s as %s = %v, %v", user, role, ok, err)
	}
	return rg.doc(t, user)
}

func (rg *rig) doc(t *testing.T, user string) domain.Member {
	t.Helper()
	docs, err := rg.rooms.MembersOf(t.Context(), group, []string{user})
	if err != nil || len(docs) != 1 {
		t.Fatalf("doc of %s = %v, %v", user, docs, err)
	}
	return docs[0]
}
