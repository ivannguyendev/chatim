package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestDirectKeyIsTheSameForBothOrders(t *testing.T) {
	if got, want := domain.DirectKey("acme", "minh", "lan"), "acme│lan│minh"; got != want {
		t.Fatalf("DirectKey(minh, lan) = %q, want %q", got, want)
	}
	if domain.DirectKey("acme", "lan", "minh") != domain.DirectKey("acme", "minh", "lan") {
		t.Fatal("DirectKey depends on the order of the pair")
	}
	if domain.DirectKey("acme", "a", "b-c") == domain.DirectKey("acme", "a-b", "c") {
		t.Fatal("DirectKey of different pairs collides")
	}
}

func TestNewDirectRoomHasTwoPlainMembersAndNoOwner(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	room, members, err := domain.NewDirectRoom("acme", "minh", "lan", now, 8812)
	if err != nil {
		t.Fatalf("NewDirectRoom: %v", err)
	}
	wantRoom := domain.Room{ID: 8812, Tenant: "acme", Type: domain.RoomDM, CreatedBy: "minh", CreatedAt: now, MemberCount: 2, DMKey: "acme│lan│minh"}
	if room != wantRoom {
		t.Errorf("room = %+v, want %+v", room, wantRoom)
	}
	member := func(user string) domain.Member {
		return domain.Member{
			Room: 8812, Tenant: "acme", User: user, Role: domain.RoleMember, JoinedAt: now, State: domain.MemberActive, Ver: 1,
			RequestID: "8812-created", UpdatedAt: now, UpdatedBy: "minh", LastChangeAt: now,
		}
	}
	if want := []domain.Member{member("minh"), member("lan")}; !slices.Equal(members, want) {
		t.Errorf("members = %+v, want %+v", members, want)
	}
}

func TestNewDirectRoomRules(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name, tenant, caller, other, field string
	}{
		{"bad tenant", "Acme", "lan", "minh", "tenant"},
		{"bad caller", "acme", "l.an", "minh", "user"},
		{"bad other", "acme", "lan", "", "user"},
		{"other with a separator", "acme", "lan", "a│b", "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := domain.NewDirectRoom(tt.tenant, tt.caller, tt.other, now, 7)
			if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field) {
				t.Fatalf("NewDirectRoom = %v, want ErrInvalidArgument naming %q", err, tt.field)
			}
		})
	}
}

func TestNewDirectRoomWithYourselfIsRefused(t *testing.T) {
	if _, _, err := domain.NewDirectRoom("acme", "lan", "lan", time.Unix(1, 0), 7); !errors.Is(err, domain.ErrSelfDirect) {
		t.Fatalf("NewDirectRoom(lan, lan) = %v, want ErrSelfDirect", err)
	}
}

func TestJoinGivesTheOwnerRoleOnlyToItsOwner(t *testing.T) {
	at := time.Unix(500, 0)
	j := domain.Join{Room: 7, Tenant: "acme", RequestID: "7-created", By: "alice", At: at, Owner: "alice"}
	if got := j.Apply(domain.Member{}, "alice"); got.Role != domain.RoleOwner {
		t.Fatalf("Apply(owner) role = %s, want owner", got.Role)
	}
	if got := j.Apply(domain.Member{}, "bob"); got.Role != domain.RoleMember {
		t.Fatalf("Apply(bob) role = %s, want member", got.Role)
	}
}
