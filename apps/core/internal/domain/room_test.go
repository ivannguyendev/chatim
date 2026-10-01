package domain_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParseRoomType(t *testing.T) {
	tests := []struct {
		in   string
		want domain.RoomType
		ok   bool
	}{
		{"dm", domain.RoomDM, true},
		{"group", domain.RoomGroup, true},
		{"", "", false},
		{"channel", "", false},
		{"DM", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := domain.ParseRoomType(tt.in)
			if tt.ok != (err == nil) || got != tt.want {
				t.Fatalf("ParseRoomType(%q) = %q, %v; want %q, ok=%v", tt.in, got, err, tt.want, tt.ok)
			}
			if err != nil && !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Errorf("ParseRoomType(%q) error %v is not ErrInvalidArgument", tt.in, err)
			}
		})
	}
}

func TestNewRoomBuildsRoomAndMembers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	room, members, err := domain.NewRoom("acme", "alice", domain.RoomGroup, "Team", []string{"bob", "alice", "bob", "carol"}, now, 42)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	wantRoom := domain.Room{ID: 42, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: now, MemberCount: 3}
	if room != wantRoom {
		t.Errorf("room = %+v, want %+v", room, wantRoom)
	}
	wantMembers := []domain.Member{
		{Room: 42, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: now},
		{Room: 42, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: now},
		{Room: 42, Tenant: "acme", User: "carol", Role: domain.RoleMember, JoinedAt: now},
	}
	if !slices.Equal(members, wantMembers) {
		t.Errorf("members = %+v, want %+v", members, wantMembers)
	}
}

func TestNewRoomRules(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	users := func(n int) []string {
		out := make([]string, 0, n)
		out = append(out, "alice")
		for i := 1; i < n; i++ {
			out = append(out, "u"+strconv.Itoa(i))
		}
		return out
	}
	tests := []struct {
		name    string
		tenant  string
		creator string
		typ     domain.RoomType
		roomNm  string
		members []string
		field   string
	}{
		{"dm ok without name", "acme", "alice", domain.RoomDM, "", []string{"alice", "bob"}, ""},
		{"dm duplicates collapse to two", "acme", "alice", domain.RoomDM, "", []string{"alice", "bob", "bob", "alice"}, ""},
		{"group of creator only", "acme", "alice", domain.RoomGroup, "Solo", []string{"alice"}, ""},
		{"group of 5000", "acme", "alice", domain.RoomGroup, "Big", users(5000), ""},
		{"name of 128 runes", "acme", "alice", domain.RoomGroup, strings.Repeat("ñ", 128), []string{"alice"}, ""},
		{"bad tenant", "Acme", "alice", domain.RoomGroup, "T", []string{"alice"}, "tenant"},
		{"bad creator", "acme", "al.ice", domain.RoomGroup, "T", []string{"al.ice"}, "user"},
		{"bad member", "acme", "alice", domain.RoomGroup, "T", []string{"alice", "b b"}, "user"},
		{"unknown type", "acme", "alice", domain.RoomType("channel"), "T", []string{"alice"}, "type"},
		{"creator missing", "acme", "alice", domain.RoomGroup, "T", []string{"bob"}, "members"},
		{"no members", "acme", "alice", domain.RoomGroup, "T", nil, "members"},
		{"dm of one", "acme", "alice", domain.RoomDM, "", []string{"alice", "alice"}, "members"},
		{"dm of three", "acme", "alice", domain.RoomDM, "", []string{"alice", "bob", "carol"}, "members"},
		{"group of 5001", "acme", "alice", domain.RoomGroup, "Big", users(5001), "members"},
		{"group without name", "acme", "alice", domain.RoomGroup, "", []string{"alice"}, "name"},
		{"group with blank name", "acme", "alice", domain.RoomGroup, " \t ", []string{"alice"}, "name"},
		{"name of 129 runes", "acme", "alice", domain.RoomGroup, strings.Repeat("ñ", 129), []string{"alice"}, "name"},
		{"name not utf-8", "acme", "alice", domain.RoomGroup, "T\xff", []string{"alice"}, "name"},
		{"dm name not utf-8", "acme", "alice", domain.RoomDM, "\xc3", []string{"alice", "bob"}, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room, members, err := domain.NewRoom(tt.tenant, tt.creator, tt.typ, tt.roomNm, tt.members, now, 7)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("NewRoom = %v, want nil", err)
				}
				if room.MemberCount != len(members) {
					t.Errorf("MemberCount = %d, len(members) = %d", room.MemberCount, len(members))
				}
				return
			}
			if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field) {
				t.Fatalf("NewRoom = %v, want ErrInvalidArgument naming %q", err, tt.field)
			}
		})
	}
}
