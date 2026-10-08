package mutate_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
