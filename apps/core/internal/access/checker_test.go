package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const room = 4242

func rooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rs := memstore.NewRooms()
	at := time.Now().UTC()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: at, MemberCount: 1}
	m := domain.Member{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at}
	if err := rs.Create(t.Context(), r, []domain.Member{m}); err != nil {
		t.Fatalf("create room: %v", err)
	}
	return rs
}

func TestCheckerEnforcesTenantAndMembershipBeforeThePolicy(t *testing.T) {
	asked := 0
	policy := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return nil })
	c, err := access.NewChecker(rooms(t), policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	cases := []struct {
		name         string
		tenant, user string
		room         uint64
		want         error
	}{
		{"unknown room", "acme", "alice", 1, apperr.ErrNotFound},
		{"other tenant", "other", "alice", room, apperr.ErrNotFound},
		{"not a member", "acme", "mallory", room, apperr.ErrPermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Authorize(t.Context(), access.ReadHistory, tc.tenant, tc.user, tc.room); !errors.Is(err, tc.want) {
				t.Fatalf("Authorize = %v, want %v", err, tc.want)
			}
		})
	}
	if asked != 0 {
		t.Fatalf("policy asked %d times before the invariants passed, want 0", asked)
	}
}

func TestCheckerPassesTheFullRequestToThePolicy(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if _, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Authorize = %v, want ErrPermissionDenied", err)
	}
	if got.Action != access.ReadHistory || got.User != "alice" || got.Room.ID != room || got.Member.Role != domain.RoleOwner {
		t.Fatalf("policy saw %+v, want read_history by owner alice in room %d", got, room)
	}
}

func TestNilPolicyAllowsMembers(t *testing.T) {
	c, err := access.NewChecker(rooms(t), nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room)
	if err != nil || req.Member.User != "alice" {
		t.Fatalf("Authorize = %+v, %v; want alice allowed", req, err)
	}
	if _, err := access.NewChecker(nil, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewChecker(nil) = %v, want ErrInvalidArgument", err)
	}
}
