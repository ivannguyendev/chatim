package mutate_test

import (
	"strconv"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
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

func (rg *rig) memberCount(t *testing.T) domain.MemberCount {
	t.Helper()
	r, err := rg.rooms.Get(t.Context(), group)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	return domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer}
}

func (rg *rig) post(t *testing.T, seq uint64) {
	t.Helper()
	m := domain.Message{Room: group, Seq: seq, Tenant: tenant, From: "owen", Kind: domain.KindText, Text: "hi", CID: "g-" + strconv.FormatUint(seq, 10), CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert group seq %d: %+v", seq, res)
	}
}

func (rg *rig) restart(t *testing.T) {
	t.Helper()
	rg.requests = newRequests(t, rg.registry)
	rg.m = rg.build(t, rg.deps(t, nil))
}

func (rg *rig) memberEvents() []string {
	_, events := rg.events.list()
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = describe(ev)
	}
	return out
}

func describe(ev *chatimv1.Event) string {
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MemberAdded:
		return "added " + p.MemberAdded.GetUser()
	case *chatimv1.Event_MemberRemoved:
		return "removed " + p.MemberRemoved.GetUser()
	case *chatimv1.Event_MemberRoleChanged:
		return "role " + p.MemberRoleChanged.GetUser()
	case *chatimv1.Event_MemberPriorityChanged:
		return "priority " + p.MemberPriorityChanged.GetUser()
	case *chatimv1.Event_MemberCountChanged:
		return "count " + strconv.Itoa(int(p.MemberCountChanged.GetMemberCount()))
	default:
		return ev.GetId()
	}
}

func add(user, requestID string, users ...string) mutate.AddMembersCmd {
	return mutate.AddMembersCmd{Tenant: tenant, User: user, Room: group, Users: users, RequestID: requestID}
}

func remove(user, target string) mutate.RemoveMemberCmd {
	return mutate.RemoveMemberCmd{Tenant: tenant, User: user, Room: group, Target: target}
}

func leave(user string) mutate.LeaveRoomCmd {
	return mutate.LeaveRoomCmd{Tenant: tenant, User: user, Room: group}
}

func setRole(user, target string, role domain.Role) mutate.ChangeRoleCmd {
	return mutate.ChangeRoleCmd{Tenant: tenant, User: user, Room: group, Target: target, Role: role}
}

func setPriority(user, target string, priority int32) mutate.SetPriorityCmd {
	return mutate.SetPriorityCmd{Tenant: tenant, User: user, Room: group, Target: target, Priority: priority}
}
