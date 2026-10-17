package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	mentionRoom   = uint64(77)
	mentionTenant = "acme"
)

var mentionJoined = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

type fakeVisible struct {
	asked []store.MsgKey
	got   map[store.MsgKey]domain.Message
}

func (f *fakeVisible) VisibleMessages(_ context.Context, _, _ string, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error) {
	f.asked = keys
	return f.got, nil
}

func newMentionCore(t *testing.T) (mentionCore, *memstore.Rooms, *memstore.Messages) {
	t.Helper()
	rooms, msgs := memstore.NewRooms(), memstore.NewMessages()
	members := []domain.Member{
		{Room: mentionRoom, Tenant: mentionTenant, User: "alice", Role: domain.RoleOwner, JoinedAt: mentionJoined},
		{Room: mentionRoom, Tenant: mentionTenant, User: "bob", Role: domain.RoleMember, JoinedAt: mentionJoined.Add(time.Second)},
		{Room: mentionRoom, Tenant: mentionTenant, User: "carol", Role: domain.RoleMember, JoinedAt: mentionJoined.Add(2 * time.Second)},
	}
	r := domain.Room{ID: mentionRoom, Tenant: mentionTenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: mentionJoined, MemberCount: 3}
	if err := rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := roomcache.NewInfoCache(16)
	if err != nil {
		t.Fatalf("NewInfoCache: %v", err)
	}
	checker, err := access.NewChecker(rooms, access.DefaultPolicy{})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return mentionCore{members: rooms, messages: msgs, infos: rooms, info: info, visible: &fakeVisible{}, access: checker}, rooms, msgs
}

func TestMentionCoreListsMembersFromTheStartForAZeroCursor(t *testing.T) {
	c, _, _ := newMentionCore(t)
	first, err := c.ListMembers(t.Context(), mentionRoom, mentionapp.MemberCursor{}, 2)
	if err != nil || len(first) != 2 || first[0].User != "alice" || first[0].Role != "owner" || !first[0].Active || first[1].User != "bob" {
		t.Fatalf("first page = %+v, %v; want alice (owner) then bob", first, err)
	}
	rest, err := c.ListMembers(t.Context(), mentionRoom, first[1].Cursor(), 2)
	if err != nil || len(rest) != 1 || rest[0].User != "carol" || !rest[0].JoinedAt.Equal(mentionJoined.Add(2*time.Second)) {
		t.Fatalf("next page = %+v, %v; want carol", rest, err)
	}
}

func TestMentionCoreReadsMembersRoomsAndIDs(t *testing.T) {
	c, _, _ := newMentionCore(t)
	got, err := c.MembersOf(t.Context(), mentionRoom, []string{"bob", "nobody"})
	if err != nil || len(got) != 1 || got[0].User != "bob" || got[0].Role != "member" || !got[0].Active {
		t.Fatalf("MembersOf = %+v, %v; want bob only", got, err)
	}
	ids, err := c.RoomMemberIDs(t.Context(), mentionRoom)
	slices.Sort(ids)
	if err != nil || !slices.Equal(ids, []string{"alice", "bob", "carol"}) {
		t.Fatalf("RoomMemberIDs = %v, %v", ids, err)
	}
	rooms, err := c.RoomsOf(t.Context(), mentionTenant, "bob")
	if err != nil || len(rooms) != 1 || rooms[0].Room != mentionRoom || !rooms[0].JoinedAt.Equal(mentionJoined.Add(time.Second)) {
		t.Fatalf("RoomsOf = %+v, %v", rooms, err)
	}
}

func TestMentionCoreCountsMembersThroughTheInfoCache(t *testing.T) {
	c, _, _ := newMentionCore(t)
	for range 2 {
		if n, err := c.RoomMemberCount(t.Context(), mentionRoom); err != nil || n != 3 {
			t.Fatalf("RoomMemberCount = %d, %v; want 3", n, err)
		}
	}
	if c.info.Hits() != 1 || c.info.Misses() != 1 {
		t.Fatalf("info cache hits %d misses %d, want one load then one hit", c.info.Hits(), c.info.Misses())
	}
	if _, err := c.RoomMemberCount(t.Context(), 999); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("RoomMemberCount(missing) = %v, want not found", err)
	}
}

func TestMentionCoreAuthorizesThroughTheChecker(t *testing.T) {
	c, _, _ := newMentionCore(t)
	if err := c.Authorize(t.Context(), mentionTenant, "bob", mentionRoom, mentionapp.ReadMembers); err != nil {
		t.Fatalf("Authorize(member) = %v", err)
	}
	if err := c.Authorize(t.Context(), mentionTenant, "zed", mentionRoom, mentionapp.ReadHistory); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Authorize(stranger) = %v, want permission denied", err)
	}
	if err := c.Authorize(t.Context(), "other", "bob", mentionRoom, mentionapp.ReadHistory); err == nil {
		t.Fatal("Authorize(other tenant) = nil, want a refusal")
	}
}
