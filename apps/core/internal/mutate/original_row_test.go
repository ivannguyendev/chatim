package mutate_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type appendLog struct {
	*memstore.Edits
	mu   sync.Mutex
	seen []uint32
}

func (a *appendLog) Append(ctx context.Context, e domain.Edit) error {
	a.mu.Lock()
	a.seen = append(a.seen, e.Version)
	a.mu.Unlock()
	return a.Edits.Append(ctx, e)
}

func (a *appendLog) versions() []uint32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.seen)
}

func (rg *rig) original(t *testing.T, seq uint64) domain.Edit {
	t.Helper()
	row, err := rg.edits.At(t.Context(), key(seq), 0)
	if err != nil {
		t.Fatalf("original row of seq %d: %v", seq, err)
	}
	return row
}

func TestFirstEditWritesTheOriginalRowOnce(t *testing.T) {
	rg := newRig(t, nil)
	sent := rg.send(t, 1, "bob", "hello")
	if _, err := rg.m.Edit(t.Context(), edit("bob", 1, 0, "hello there")); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	want := domain.Edit{Room: room, Seq: 1, Version: 0, Kind: domain.EditOriginal, Tenant: tenant, By: "bob", Text: "hello", At: sent.CreatedAt}
	if got := rg.original(t, 1); !sameEdit(got, want) {
		t.Fatalf("original row = %+v, want %+v", got, want)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 || facts[0].Version != 1 {
		t.Fatalf("facts after 0 = %+v, want only v1", facts)
	}
}

func TestARetriedFirstEditKeepsOneOriginalRow(t *testing.T) {
	rg := newRig(t, nil)
	sent := rg.send(t, 1, "alice", "v0")
	crashed := domain.Edit{Room: room, Seq: 1, Version: 0, Kind: domain.EditOriginal, Tenant: tenant, By: "alice", Text: "v0", At: sent.CreatedAt}
	if err := rg.edits.Append(t.Context(), crashed); err != nil {
		t.Fatalf("Append original: %v", err)
	}
	for range 2 {
		if got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil || got.Version != 1 {
			t.Fatalf("Edit = %+v, %v; want v1", got, err)
		}
		rg.now = rg.now.Add(time.Second)
	}
	if got := rg.original(t, 1); !sameEdit(got, crashed) {
		t.Fatalf("original row = %+v, want the one already there %+v", got, crashed)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want one v1", facts)
	}
}

func TestLaterEditsWriteNoOriginalRow(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	appended := &appendLog{Edits: rg.edits}
	rg.m = rg.mutator(t, nil, appended)
	for base, text := range []string{"v1", "v2", "v3"} {
		if _, err := rg.m.Edit(t.Context(), edit("alice", 1, uint32(base), text)); err != nil {
			t.Fatalf("Edit base %d: %v", base, err)
		}
	}
	if got := appended.versions(); !slices.Equal(got, []uint32{0, 1, 2, 3}) {
		t.Fatalf("appended versions = %v, want the original row only before v1", got)
	}
	if got := rg.original(t, 1); got.Text != "v0" || got.Kind != domain.EditOriginal {
		t.Fatalf("original row = %+v, want the sent text", got)
	}
}
