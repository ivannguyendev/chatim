package mutate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestTheDefaultPolicyLetsOnlyTheAuthorChangeAMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	rg.send(t, 2, "bob", "b")
	for _, c := range []mutate.EditCmd{edit("bob", 1, 0, "x"), edit("alice", 2, 0, "x")} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, access.ErrDenied) || !errors.Is(err, apperr.ErrPermissionDenied) {
			t.Fatalf("%s edits seq %d = %v, want ErrDenied", c.User, c.Seq, err)
		}
	}
	for _, c := range []mutate.DeleteCmd{del("bob", 1, 0), del("carol", 2, 0), del("alice", 2, 0)} {
		if _, err := rg.m.Delete(t.Context(), c); !errors.Is(err, access.ErrDenied) {
			t.Fatalf("%s deletes seq %d = %v, want ErrDenied even for the room owner", c.User, c.Seq, err)
		}
	}
	if len(rg.facts(t, 1))+len(rg.facts(t, 2)) != 0 {
		t.Fatalf("a refused change wrote a fact")
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("a refused change enqueued %v", events)
	}
	if got, err := rg.m.Delete(t.Context(), del("bob", 2, 0)); err != nil || !got.Deleted || got.Version != 1 {
		t.Fatalf("author delete = %+v, %v; want a deleted snapshot at version 1", got, err)
	}
}

func TestAPolicyCanLetTheRoomOwnerDeleteAnyMessage(t *testing.T) {
	ownerOrAuthor := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action != access.DeleteMessage || r.Member.Role == domain.RoleOwner || r.Author == r.User {
			return nil
		}
		return access.ErrDenied
	})
	rg := newRig(t, ownerOrAuthor)
	rg.send(t, 1, "bob", "b")
	rg.send(t, 2, "carol", "c")
	if _, err := rg.m.Delete(t.Context(), del("bob", 2, 0)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("bob deletes carol's message = %v, want ErrDenied", err)
	}
	got, err := rg.m.Delete(t.Context(), del("alice", 1, 0))
	if err != nil || !got.Deleted || got.Text != "" || got.Version != 1 {
		t.Fatalf("owner delete = %+v, %v; want a deleted snapshot at version 1", got, err)
	}
	if again, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil || !again.Deleted {
		t.Fatalf("owner retry = %+v, %v; want success", again, err)
	}
	_, events := rg.events.list()
	if len(events) != 2 || events[0].GetActor() != "alice" || events[0].GetMessageDeleted() == nil {
		t.Fatalf("events = %v, want msg_deleted by alice", events)
	}
}

func TestChangesAskThePolicyWithTheAuthor(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "bob", "b")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 9, 0, "x")); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Edit of a missing message = %v, want ErrMessageNotFound", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "x")); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Edit = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Delete = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.EditMessage || asked[1].Action != access.DeleteMessage {
		t.Fatalf("policy asked %+v, want edit_message then delete_message", asked)
	}
	for _, r := range asked {
		if r.User != "alice" || r.Author != "bob" || r.Member.Role != domain.RoleOwner {
			t.Fatalf("policy saw %+v, want owner alice on bob's message", r)
		}
	}
	open := newRig(t, nil)
	open.send(t, 1, "alice", "a")
	if _, err := open.m.Edit(t.Context(), edit("mallory", 1, 0, "x")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("stranger Edit = %v, want ErrNotMember", err)
	}
	other := edit("alice", 1, 0, "x")
	other.Room = 999
	if _, err := open.m.Edit(t.Context(), other); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("unknown room Edit = %v, want ErrRoomNotFound", err)
	}
}
