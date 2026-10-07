package effects_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type editRig struct {
	msgs    *memstore.Messages
	edits   *memstore.Edits
	rooms   *memstore.Rooms
	js      *publishtest.JetStream
	proj    *effects.EditProjection
	changed *effects.MessageChanged
}

func newEditRig(t *testing.T) *editRig {
	t.Helper()
	rg := &editRig{msgs: memstore.NewMessages(), edits: memstore.NewEdits(), rooms: memstore.NewRooms(), js: &publishtest.JetStream{}}
	createRoom(t, rg.rooms, room)
	proj, err := effects.NewEditProjection(effects.EditProjectionDeps{Edits: rg.edits, Messages: rg.msgs, Purger: rg.edits})
	if err != nil {
		t.Fatalf("NewEditProjection: %v", err)
	}
	changed, err := effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: rg.edits, Messages: rg.msgs, Rooms: rg.rooms, JS: rg.js},
		effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16},
	)
	if err != nil {
		t.Fatalf("NewMessageChanged: %v", err)
	}
	rg.proj, rg.changed = proj, changed
	return rg
}

func (rg *editRig) original(t *testing.T, r, seq uint64) {
	t.Helper()
	m := domain.Message{Room: r, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "v0", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d/%d: %+v", r, seq, res)
	}
}

func (rg *editRig) appendFact(t *testing.T, r, seq uint64, v uint32, kind domain.EditKind, text string) domain.Edit {
	t.Helper()
	e := domain.Edit{Room: r, Seq: seq, Version: v, Kind: kind, Tenant: tenant, By: "alice", Text: text, At: time.Now().UTC().Truncate(time.Millisecond)}
	if err := rg.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("append v%d: %v", v, err)
	}
	return e
}

func (rg *editRig) project(t *testing.T, e domain.Edit) {
	t.Helper()
	if err := rg.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit v%d: %v", e.Version, err)
	}
}

func (rg *editRig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func editRecs(r, seq uint64, versions ...uint32) []work.Record {
	out := make([]work.Record, len(versions))
	for i, v := range versions {
		out[i] = work.Record{Kind: store.EditInserted, Room: r, Seq: seq, Version: v, CommittedAt: time.Now()}
	}
	return out
}

func (brokenStore) At(context.Context, store.MsgKey, uint32) (domain.Edit, error) {
	return domain.Edit{}, errBoom
}

func (brokenStore) ApplyEdit(context.Context, domain.Edit) error { return errBoom }

func (brokenStore) PurgeText(context.Context, store.MsgKey, uint32) error { return errBoom }
