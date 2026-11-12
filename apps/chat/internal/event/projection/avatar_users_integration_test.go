package projection_test

import (
	"slices"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/chat/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func memberWire(room uint64, user string, removed bool) *chatimv1.Event {
	ev := envelope(room, 0, 0, at(1))
	ev.Id = ev.GetId() + "-" + user
	ev.RoomType = chatimv1.RoomType_ROOM_TYPE_GROUP
	if removed {
		ev.Id += "-out"
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{User: user}}
	} else {
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{User: user}}
	}
	return ev
}

func runAvatarConsumer(t *testing.T, js jetstream.JetStream, cfg projection.Config, rooms *memstore.Rooms, last uint64) {
	t.Helper()
	c := built(projection.NewConsumer([]projection.Projection{projection.NewAvatarUsers(rooms)}, projection.NewSource(js, cfg), cfg, (&testlog.Sink{}).Logger()))(t)
	result := make(chan error, 1)
	go func() { result <- c.Run(t.Context()) }()
	awaitSettled(t, js, cfg, last)
	if err := c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestAvatarUsersFollowRealMemberEvents(t *testing.T) {
	js, cfg := realEventStream(t)
	if err := projection.EnsureConsumer(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	users := []string{"an", "binh", "chi", "dung", "em"}
	members := make([]domain.Member, len(users))
	for i, u := range users {
		members[i] = domain.Member{Room: roomA, Tenant: tenant, User: u, Role: domain.RoleMember, JoinedAt: baseTime}
	}
	members[0].Role = domain.RoleOwner
	rooms := memstore.NewRooms()
	if err := rooms.Create(t.Context(), domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup, CreatedAt: baseTime, MemberCount: len(users)}, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
	events := make([]*chatimv1.Event, len(users))
	for i, u := range users {
		events[i] = memberWire(roomA, u, false)
	}
	publishAll(t, js, cfg, events)
	runAvatarConsumer(t, js, cfg, rooms, uint64(len(events)))
	first := avatarUsersOf(t, rooms, users)
	gone, err := rooms.MembersOf(t.Context(), roomA, first[:1])
	if err != nil || len(gone) != 1 {
		t.Fatalf("MembersOf = %v, %v", gone, err)
	}
	next := gone[0].Next(gone[0].Role, domain.MemberRemoved, gone[0].Priority, "req-kick", "an", at(5))
	if ok, err := rooms.ApplyMember(t.Context(), gone[0], next); err != nil || !ok {
		t.Fatalf("ApplyMember = %v, %v", ok, err)
	}
	publishAll(t, js, cfg, []*chatimv1.Event{memberWire(roomA, first[0], true)})
	runAvatarConsumer(t, js, cfg, rooms, uint64(len(events)+1))
	if after := avatarUsersOf(t, rooms, users); slices.Contains(after, first[0]) {
		t.Fatalf("avatar users %v still hold the removed %s", after, first[0])
	}
}

func avatarUsersOf(t *testing.T, rooms *memstore.Rooms, users []string) []string {
	t.Helper()
	r, err := rooms.Get(t.Context(), roomA)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.AvatarUsers) != domain.MaxAvatarUsers {
		t.Fatalf("avatar users %v, want %d", r.AvatarUsers, domain.MaxAvatarUsers)
	}
	for _, u := range r.AvatarUsers {
		if !slices.Contains(users, u) {
			t.Fatalf("avatar users %v hold %s who never joined", r.AvatarUsers, u)
		}
	}
	return r.AvatarUsers
}
