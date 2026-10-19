package access_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type countingFinder struct {
	calls int
	room  uint64
	keys  []store.MsgKey
	found []domain.Message
	err   error
}

func (f *countingFinder) Find(_ context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	f.calls++
	f.room = room
	f.keys = append([]store.MsgKey(nil), keys...)
	return f.found, f.err
}

var targetKey = store.MsgKey{Room: room, Thread: 0, Seq: 7}

func bobsMessage() domain.Message {
	return domain.Message{Room: room, Seq: 7, Tenant: "acme", From: "bob", Kind: domain.KindSystem, Text: "hi"}
}

func TestTargetRefusesANonMemberBeforeFindingTheMessage(t *testing.T) {
	asked := 0
	policy := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return nil })
	c, err := access.NewChecker(rooms(t), policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	finder := &countingFinder{found: []domain.Message{bobsMessage()}}
	if _, _, err := c.Target(t.Context(), finder, access.EditMessage, "acme", "mallory", targetKey); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Target stranger = %v, want ErrPermissionDenied", err)
	}
	if finder.calls != 0 || asked != 0 {
		t.Fatalf("Target stranger made %d finds and %d policy calls, want 0 and 0", finder.calls, asked)
	}
}

func TestTargetReportsAMissingMessage(t *testing.T) {
	asked := 0
	policy := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return nil })
	c, err := access.NewChecker(rooms(t), policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	finder := &countingFinder{}
	if _, _, err := c.Target(t.Context(), finder, access.EditMessage, "acme", "alice", targetKey); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Target missing = %v, want ErrMessageNotFound", err)
	}
	if finder.calls != 1 || finder.room != room || len(finder.keys) != 1 || finder.keys[0] != targetKey || asked != 0 {
		t.Fatalf("Target missing found %d times in room %d with %v and asked the policy %d times, want one find of %v and no policy call", finder.calls, finder.room, finder.keys, asked, targetKey)
	}
}

func TestTargetReturnsTheFinderErrorAsItIs(t *testing.T) {
	c, err := access.NewChecker(rooms(t), nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	boom := errors.New("store down")
	finder := &countingFinder{err: boom}
	if _, _, err := c.Target(t.Context(), finder, access.EditMessage, "acme", "alice", targetKey); !errors.Is(err, boom) || err.Error() != boom.Error() {
		t.Fatalf("Target store failure = %v, want the store error unwrapped", err)
	}
}

func TestTargetAsksThePolicyWithTheMessageAuthorAndKind(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	finder := &countingFinder{found: []domain.Message{bobsMessage()}}
	if _, _, err := c.Target(t.Context(), finder, access.DeleteMessage, "acme", "alice", targetKey); !errors.Is(err, access.ErrDenied) || err.Error() != access.ErrDenied.Error() {
		t.Fatalf("Target denied = %v, want ErrDenied unwrapped", err)
	}
	if got.Action != access.DeleteMessage || got.User != "alice" || got.Author != "bob" || got.Kind != domain.KindSystem || got.Room.ID != room || got.Member.Role != domain.RoleOwner {
		t.Fatalf("policy saw %+v, want delete_message by owner alice on bob's system message", got)
	}
}

func TestTargetReturnsTheRequestAndTheMessageWhenAllowed(t *testing.T) {
	c, err := access.NewChecker(rooms(t), access.AllowMembers{})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	finder := &countingFinder{found: []domain.Message{bobsMessage()}}
	req, msg, err := c.Target(t.Context(), finder, access.ReactMessage, "acme", "alice", targetKey)
	if err != nil {
		t.Fatalf("Target = %v, want nil", err)
	}
	if req.Action != access.ReactMessage || req.User != "alice" || req.Author != "bob" || req.Kind != domain.KindSystem || req.Room.ID != room || req.Member.User != "alice" {
		t.Fatalf("Target request = %+v, want react_message by alice on bob's system message", req)
	}
	if msg.From != "bob" || msg.Seq != 7 || msg.Text != "hi" {
		t.Fatalf("Target message = %+v, want bob's seq 7", msg)
	}
	if finder.calls != 1 {
		t.Fatalf("Target found %d times, want 1", finder.calls)
	}
}
