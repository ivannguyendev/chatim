package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEditProjectionDeclaresItsPolicy(t *testing.T) {
	e := newEditRig(t).proj.Effect()
	if e.Name != effects.EditProjectionName || e.Delay != 0 || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.EditProjectionName)
	}
}

func TestEditProjectionAppliesAFactTheFastPathMissed(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	f := rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	for range 2 {
		if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	got := rg.stored(t, 1)
	if got.Version != 1 || got.Text != "v1" || got.Deleted || !got.EditedAt.Equal(f.At) {
		t.Fatalf("stored = %+v, want v1 projected", got)
	}
	if rg.proj.Dropped() != 0 {
		t.Fatalf("dropped = %d", rg.proj.Dropped())
	}
}

func TestEditProjectionPurgesOlderTextOnDelete(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	rg.appendFact(t, room, 1, 2, domain.EditDelete, "")
	if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 2, 1)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.stored(t, 1); got.Version != 2 || !got.Deleted || got.Text != "" {
		t.Fatalf("stored = %+v, want deleted at v2 and the late v1 ignored", got)
	}
	v1, err := rg.edits.At(t.Context(), store.MsgKey{Room: room, Seq: 1}, 1)
	if err != nil || v1.Text != "" {
		t.Fatalf("v1 = %+v, %v; want its text purged", v1, err)
	}
}

func TestEditProjectionDropsAMissingFact(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 7)); !allNil(errs, 1) {
		t.Fatalf("errs = %v, want nil so the record is not retried", errs)
	}
	if rg.proj.Dropped() != 1 || rg.stored(t, 1).Version != 0 {
		t.Fatalf("dropped %d, stored %+v; want 1 and the message untouched", rg.proj.Dropped(), rg.stored(t, 1))
	}
}

func TestEditProjectionRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewEditProjection(effects.EditProjectionDeps{Edits: brokenStore{}, Messages: brokenStore{}, Purger: brokenStore{}})
	if err != nil {
		t.Fatalf("NewEditProjection: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 1)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestEditProjectionSkipsTheOriginalRow(t *testing.T) {
	eff, err := effects.NewEditProjection(effects.EditProjectionDeps{Edits: brokenStore{}, Messages: brokenStore{}, Purger: brokenStore{}})
	if err != nil {
		t.Fatalf("NewEditProjection: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 0)); !allNil(errs, 1) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want nil without reading the store and no drop", errs, eff.Dropped())
	}
}

func TestNewEditProjectionRejectsMissingDeps(t *testing.T) {
	edits, msgs := memstore.NewEdits(), memstore.NewMessages()
	for name, deps := range map[string]effects.EditProjectionDeps{
		"no edits":    {Messages: msgs, Purger: edits},
		"no messages": {Edits: edits, Purger: edits},
		"no purger":   {Edits: edits, Messages: msgs},
	} {
		if _, err := effects.NewEditProjection(deps); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewEditProjection = %v, want ErrInvalidArgument", name, err)
		}
	}
}
