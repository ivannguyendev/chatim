package mutate_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func replyTo(parent, seq uint64) domain.Reply {
	return domain.Reply{
		Parent: domain.MsgKey{Room: room, Seq: parent}, Room: room, Seq: seq, Tenant: tenant, From: "bob", At: created,
	}
}

func (rg *rig) addReply(t *testing.T, parent, seq uint64) {
	t.Helper()
	if _, err := rg.reactions.AddReply(t.Context(), replyTo(parent, seq)); err != nil {
		t.Fatalf("AddReply %d -> %d: %v", seq, parent, err)
	}
}

func TestDeleteRefusesAMessageThatStillHasReplies(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "parent")
	rg.send(t, 2, "bob", "re")
	rg.addReply(t, 1, 2)
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); !errors.Is(err, domain.ErrHasReplies) {
		t.Fatalf("delete of a replied message = %v, want ErrHasReplies", err)
	}
	if got := rg.stored(t, 1); got.Deleted || len(rg.facts(t, 1)) != 0 {
		t.Fatalf("refused delete changed the message: %+v, facts %+v", got, rg.facts(t, 1))
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "still here")); err != nil {
		t.Fatalf("edit of a replied message = %v, want success", err)
	}
	if _, err := rg.reactions.RemoveReply(t.Context(), replyTo(1, 2), created); err != nil {
		t.Fatalf("RemoveReply: %v", err)
	}
	if got, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); err != nil || !got.Deleted {
		t.Fatalf("delete after the last reply went = %+v, %v; want deleted", got, err)
	}
}

func TestARetriedDeleteSucceedsEvenWhenRepliesCameLater(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "parent")
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rg.send(t, 2, "bob", "re")
	rg.addReply(t, 1, 2)
	if got, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil || !got.Deleted {
		t.Fatalf("retried delete = %+v, %v; want the applied delete", got, err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("new delete of a deleted message = %v, want ErrMessageDeleted", err)
	}
}
