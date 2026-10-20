package message_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestTheAuthorCannotChangeALockedKind(t *testing.T) {
	rg := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}})
	rg.send(t, 1, "bob", "b")
	if _, err := rg.m.Edit(t.Context(), edit("bob", 1, 0, "x")); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("author edit of a locked kind = %v, want ErrDenied", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("bob", 1, 0)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("author delete of a locked kind = %v, want ErrDenied", err)
	}
	if facts := rg.facts(t, 1); len(facts) != 0 {
		t.Fatalf("a locked change wrote facts %+v", facts)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("a locked change enqueued %v", events)
	}
	if got := rg.stored(t, 1); got.Version != 0 || got.Deleted || got.Text != "b" {
		t.Fatalf("stored = %+v, want the untouched original", got)
	}
}

func TestLockedSystemKindBlocksOnlySystemMessages(t *testing.T) {
	rg := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindSystem}})
	sys := domain.Message{Room: room, Seq: 1, Tenant: tenant, From: "bob", Kind: domain.KindSystem, Text: "lan left", CID: "sys-1", CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{sys}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert system message: %+v", res)
	}
	rg.send(t, 2, "bob", "b")
	if _, err := rg.m.Edit(t.Context(), edit("bob", 1, 0, "x")); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("edit of a locked system message = %v, want ErrDenied", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("bob", 1, 0)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("delete of a locked system message = %v, want ErrDenied", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("bob", 2, 0, "x")); err != nil {
		t.Fatalf("edit of a text message with system locked = %v, want nil", err)
	}
}
