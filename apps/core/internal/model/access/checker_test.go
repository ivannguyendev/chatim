package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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

func TestNilPolicyUsesTheDefaultPolicy(t *testing.T) {
	c, err := access.NewChecker(rooms(t), nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room)
	if err != nil || req.Member.User != "alice" {
		t.Fatalf("Authorize = %+v, %v; want alice allowed", req, err)
	}
	del, err := c.Admit(t.Context(), access.DeleteMessage, "acme", "alice", room)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	del.Author = "bob"
	if err := c.Allow(t.Context(), del); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("owner alice deletes bob's message = %v, want ErrDenied", err)
	}
	if _, err := access.NewChecker(nil, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewChecker(nil) = %v, want ErrInvalidArgument", err)
	}
}

func TestAdmitChecksTheRoomWithoutThePolicy(t *testing.T) {
	asked := 0
	deny := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Admit(t.Context(), access.EditMessage, "acme", "alice", room)
	if err != nil || asked != 0 {
		t.Fatalf("Admit = %v after %d policy calls, want nil after 0", err, asked)
	}
	if req.Action != access.EditMessage || req.User != "alice" || req.Author != "" || req.Room.ID != room || req.Member.Role != domain.RoleOwner {
		t.Fatalf("Admit = %+v, want edit_message by owner alice without an author", req)
	}
	if _, err := c.Admit(t.Context(), access.EditMessage, "acme", "mallory", room); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Admit stranger = %v, want ErrPermissionDenied", err)
	}
}

func TestAllowPassesTheRequestWithTheAuthor(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Admit(t.Context(), access.DeleteMessage, "acme", "alice", room)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	req.Author = "bob"
	if err := c.Allow(t.Context(), req); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Allow = %v, want ErrPermissionDenied", err)
	}
	if got.Action != access.DeleteMessage || got.User != "alice" || got.Author != "bob" || got.Room.ID != room || got.Member.Role != domain.RoleOwner {
		t.Fatalf("policy saw %+v, want delete_message by owner alice on bob's message", got)
	}
}
