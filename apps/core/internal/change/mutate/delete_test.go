package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestDeletePurgesTheTextOfEarlierVersions(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	for _, c := range []mutate.EditCmd{edit("alice", 1, 0, "v1"), edit("alice", 1, 1, "v2")} {
		if _, err := rg.m.Edit(t.Context(), c); err != nil {
			t.Fatalf("Edit base %d: %v", c.BaseVersion, err)
		}
	}
	got, err := rg.m.Delete(t.Context(), del("alice", 1, 2))
	if err != nil || !got.Deleted || got.Text != "" || got.Version != 3 {
		t.Fatalf("Delete = %+v, %v; want deleted at version 3", got, err)
	}
	facts := rg.facts(t, 1)
	if len(facts) != 3 || facts[2].Kind != domain.EditDelete {
		t.Fatalf("facts = %+v, want v1, v2 and a delete", facts)
	}
	for _, f := range facts {
		if f.Text != "" {
			t.Fatalf("fact v%d keeps text %q after delete", f.Version, f.Text)
		}
	}
	if _, events := rg.events.list(); len(events) != 3 || !proto.Equal(events[2], pbconv.MessageDeleted(domain.RoomGroup, got, facts[2])) {
		t.Fatalf("last event = %v, want msg_deleted v3", events)
	}
}

func TestNothingChangesADeletedMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 1, "back")); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("edit after delete = %v, want ErrMessageDeleted", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("second delete = %v, want ErrMessageDeleted", err)
	}
	if got, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil || !got.Deleted {
		t.Fatalf("retried delete = %+v, %v; want success", got, err)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 || facts[0].Kind != domain.EditDelete || facts[0].Text != "" {
		t.Fatalf("facts = %+v, want only the delete, without the original text", facts)
	}
}

func TestAFirstDeleteWritesNoOriginalRow(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	appended := &appendLog{Edits: rg.edits}
	rg.m = rg.mutator(t, nil, appended)
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := rg.edits.At(t.Context(), key(1), 0); !errors.Is(err, store.ErrEditNotFound) {
		t.Fatalf("original row after a first delete = %v, want none", err)
	}
	if got := appended.versions(); !slices.Equal(got, []uint32{1}) {
		t.Fatalf("appended versions = %v, want only the delete", got)
	}
}

func TestADeleteAfterAnEditPurgesTheOriginalRow(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	row, err := rg.edits.At(t.Context(), key(1), 0)
	if err != nil || row.Kind != domain.EditOriginal || row.Text != "" || row.By != "alice" {
		t.Fatalf("original row = %+v, %v; want it kept without text", row, err)
	}
	if facts := rg.facts(t, 1); len(facts) != 2 || facts[0].Text != "" || facts[1].Kind != domain.EditDelete {
		t.Fatalf("facts = %+v, want v1 purged and the delete", facts)
	}
}

func TestChangesOfAnUnknownMessage(t *testing.T) {
	rg := newRig(t, nil)
	if _, err := rg.m.Edit(t.Context(), edit("alice", 9, 0, "x")); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Edit = %v, want ErrMessageNotFound", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 9, 0)); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Delete = %v, want ErrMessageNotFound", err)
	}
}

func TestARefusedEventDoesNotFailTheChange(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	rg.events.err = errBoom
	if got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil || got.Version != 1 {
		t.Fatalf("Edit with a refused event = %+v, %v; want success", got, err)
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now, full.NewRequestID = nil, nil
	for name, drop := range map[string]func(d *mutate.Deps){
		"no access":     func(d *mutate.Deps) { d.Access = nil },
		"no messages":   func(d *mutate.Deps) { d.Messages = nil },
		"no edits":      func(d *mutate.Deps) { d.Edits = nil },
		"no hidden":     func(d *mutate.Deps) { d.Hidden = nil },
		"no rooms":      func(d *mutate.Deps) { d.Rooms = nil },
		"no events":     func(d *mutate.Deps) { d.Events = nil },
		"no reactions":  func(d *mutate.Deps) { d.Reactions = nil },
		"no counter":    func(d *mutate.Deps) { d.Counter = nil },
		"no pins":       func(d *mutate.Deps) { d.Pins = nil },
		"no projector":  func(d *mutate.Deps) { d.Projector = nil },
		"no members":    func(d *mutate.Deps) { d.Members = nil },
		"no requests":   func(d *mutate.Deps) { d.Requests = nil },
		"no forget":     func(d *mutate.Deps) { d.Forget = nil },
		"no timers":     func(d *mutate.Deps) { d.Timers = nil },
		"no reads":      func(d *mutate.Deps) { d.Reads = nil },
		"bad batch":     func(d *mutate.Deps) { d.Limits = mutate.Limits{MemberBatch: 1} },
		"bad pin limit": func(d *mutate.Deps) { d.Limits = mutate.Limits{PinLimit: mutate.MaxPinLimit + 1} },
		"no emojis":     func(d *mutate.Deps) { d.Limits = mutate.Limits{Emojis: []string{}} },
	} {
		d := full
		drop(&d)
		if _, err := mutate.New(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := mutate.New(full); err != nil {
		t.Fatalf("New with a default clock: %v", err)
	}
}
