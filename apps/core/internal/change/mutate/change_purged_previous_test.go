package mutate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type racingEdits struct {
	*memstore.Edits
	afterAppend func(domain.Edit)
	beforeAt    func(uint32)
}

func (e *racingEdits) Append(ctx context.Context, f domain.Edit) error {
	if err := e.Edits.Append(ctx, f); err != nil {
		return err
	}
	if hook := e.afterAppend; hook != nil && f.Version > 0 {
		e.afterAppend = nil
		hook(f)
	}
	return nil
}

func (e *racingEdits) At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	if hook := e.beforeAt; hook != nil {
		e.beforeAt = nil
		hook(version)
	}
	return e.Edits.At(ctx, key, version)
}

func (rg *rig) land(t *testing.T, f domain.Edit, withFact bool) {
	t.Helper()
	if withFact {
		if err := rg.edits.Append(t.Context(), f); err != nil {
			t.Fatalf("racing Append v%d: %v", f.Version, err)
		}
	}
	if err := rg.msgs.ApplyEdit(t.Context(), f); err != nil {
		t.Fatalf("racing ApplyEdit v%d: %v", f.Version, err)
	}
	if f.Kind == domain.EditDelete {
		if err := rg.edits.PurgeText(t.Context(), key(f.Seq), f.Version-1); err != nil {
			t.Fatalf("racing PurgeText: %v", err)
		}
	}
}

func racer(version uint32, kind domain.EditKind, text string) domain.Edit {
	return domain.Edit{Room: room, Seq: 1, Version: version, Kind: kind, Tenant: tenant, By: "alice", Text: text, At: created.Add(time.Duration(version) * time.Second)}
}

func (rg *rig) racingEdit(t *testing.T, withFacts bool) *racingEdits {
	t.Helper()
	rg.send(t, 1, "alice", "v0")
	racing := &racingEdits{Edits: rg.edits}
	racing.afterAppend = func(domain.Edit) {
		rg.land(t, racer(2, domain.EditText, "other v2"), withFacts)
		rg.land(t, racer(3, domain.EditText, "other v3"), withFacts)
	}
	rg.m = rg.mutator(t, nil, racing)
	return racing
}

func (rg *rig) pendingEdit(t *testing.T, version uint32) bool {
	t.Helper()
	states := memstore.NewEventStates(memstore.EventStateStores{Edits: rg.edits})
	docs, _, err := states.Pending(t.Context(), store.StateEdits, time.Time{}, created.Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	for _, d := range docs {
		if d.Seq == 1 && d.Ver == uint64(version) {
			return true
		}
	}
	return false
}

func TestEditWhosePreviousVersionARacingDeletePurgedIsAcked(t *testing.T) {
	rg := newRig(t, nil)
	racing := rg.racingEdit(t, true)
	racing.beforeAt = func(uint32) { rg.land(t, racer(4, domain.EditDelete, ""), true) }
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "mine"))
	if err != nil {
		t.Fatalf("Edit = %v, want the written edit acked", err)
	}
	if got.Version != 3 || got.Text != "other v3" || got.Deleted {
		t.Fatalf("snapshot = %+v, want the snapshot read after the projection", got)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none enqueued inline", events)
	}
	if !rg.pendingEdit(t, 1) {
		t.Fatal("edit fact v1 lost its pending bit, want it left for the sweeper")
	}
}

func TestEditWhosePreviousVersionHasNoRowStaysUnavailable(t *testing.T) {
	rg := newRig(t, nil)
	rg.racingEdit(t, false)
	_, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "mine"))
	if !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, store.ErrPreviousPurged) {
		t.Fatalf("Edit = %v, want UNAVAILABLE for a missing previous row", err)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}
