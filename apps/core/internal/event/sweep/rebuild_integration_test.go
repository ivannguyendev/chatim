package sweep_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func itRebuilders(t *testing.T, st *mongostore.Store) map[store.StateCollection]sweep.Rebuilder {
	t.Helper()
	proj, err := pinproj.New(st.Pins(), st)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	s := sweep.RebuilderStores{
		Messages: st, Rooms: st, Edits: st, Interactions: st.Interactions(),
		Pins: st.Pins(), PinProjector: proj, Members: st, Hidden: st.Hidden(),
	}
	rbs, err := sweep.NewRebuilders(s, eventkit.NewRoomTypes(st, 16), nil)
	if err != nil {
		t.Fatalf("NewRebuilders: %v", err)
	}
	return rbs
}

func itSweepOnce(t *testing.T, st *mongostore.Store, js jetstream.JetStream, root string) *sweep.Sweeper {
	t.Helper()
	deps := sweep.Deps{States: st.EventStates(), Checkpoints: st.SweepCheckpoints(), Owner: alwaysLeader{}, JS: js, Rebuilders: itRebuilders(t, st)}
	cfg := sweep.Config{SubjectRoot: root, MinAge: time.Millisecond, Interval: 50 * time.Millisecond, Tick: 50 * time.Millisecond, AckTimeout: 5 * time.Second}
	s, err := sweep.New(deps, cfg, (&testlog.Sink{}).Logger())
	if err != nil {
		t.Fatalf("sweep.New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	deadline := time.Now().Add(10 * time.Second)
	for s.Passes() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Passes() == 0 {
		t.Fatal("sweeper finished no pass")
	}
	return s
}

func itSeedEveryCollection(t *testing.T, st *mongostore.Store, old time.Time) (domain.ReadPosition, time.Time) {
	t.Helper()
	ctx := t.Context()
	r := domain.Room{ID: itRoom, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "ann", CreatedAt: old, MemberCount: 2}
	founders := []domain.Member{
		{Room: itRoom, Tenant: tenant, User: "ann", Role: domain.RoleOwner, JoinedAt: old},
		{Room: itRoom, Tenant: tenant, User: "bob", Role: domain.RoleMember, JoinedAt: old},
	}
	if err := st.Create(ctx, r, founders); err != nil {
		t.Fatalf("Create: %v", err)
	}
	msg := domain.Message{Room: itRoom, Seq: 1, Tenant: tenant, From: "ann", CID: "c1", Text: "hi", CreatedAt: old}
	if res := st.Insert(ctx, []domain.Message{msg}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v", res[0])
	}
	if err := st.Append(ctx, domain.Edit{Room: itRoom, Seq: 1, Kind: domain.EditOriginal, Tenant: tenant, By: "ann", Text: "hi", At: old}); err != nil {
		t.Fatalf("Append original row: %v", err)
	}
	if err := st.Append(ctx, domain.Edit{Room: itRoom, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "ann", Text: "hello", At: old.Add(time.Second)}); err != nil {
		t.Fatalf("Append edit: %v", err)
	}
	pos, moved, err := st.MarkRead(ctx, itRoom, "ann", 1)
	if err != nil || !moved {
		t.Fatalf("MarkRead = %v, %v", moved, err)
	}
	clearedAt, raised, err := st.ClearHistory(ctx, itRoom, "bob", old.Add(2*time.Second))
	if err != nil || !raised {
		t.Fatalf("ClearHistory = %v, %v", raised, err)
	}
	if _, changed, err := st.Interactions().SetReaction(ctx, domain.Reaction{Room: itRoom, Seq: 1, Tenant: tenant, User: "bob", Emoji: "👍", At: old}); err != nil || !changed {
		t.Fatalf("SetReaction = %v, %v", changed, err)
	}
	if err := st.Pins().Append(ctx, domain.PinAction{Room: itRoom, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 1, By: "ann", At: old}); err != nil {
		t.Fatalf("Append pin: %v", err)
	}
	if first, err := st.Hidden().Hide(ctx, "bob", store.MsgKey{Room: itRoom, Seq: 1}, old); err != nil || !first {
		t.Fatalf("Hide = %v, %v", first, err)
	}
	return pos, clearedAt
}

func TestSweepRebuildsEveryCollectionFlaggedByTheRealStore(t *testing.T) {
	st := itMongoStore(t)
	js, sc := itEventStream(t)
	old := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	pos, clearedAt := itSeedEveryCollection(t, st, old)
	s := itSweepOnce(t, st, js, sc.SubjectRoot)
	got := streamIDs(t, js, sc.Name)
	for _, id := range []string{
		pbconv.MessageChangeEventID(itRoom, 0, 1, 1),
		pbconv.MemberEventID(itRoom, "ann", 1), pbconv.MemberEventID(itRoom, "bob", 1),
		pbconv.ReadPositionsEventID(itRoom, []*chatimv1.ReadEntry{{User: "ann", ReadSeq: pos.Seq, ReadVer: pos.Ver}}), pbconv.ClearedEventID(itRoom, "bob", clearedAt),
		pbconv.ReactionEventID(itRoom, 0, 1, "bob", 1), pbconv.PinEventID(itRoom, 1), pbconv.HiddenEventID(itRoom, "bob", 0, 1),
	} {
		if !slices.Contains(got, id) {
			t.Fatalf("stream ids = %v, missing %s", got, id)
		}
	}
	found, err := st.Find(t.Context(), itRoom, []store.MsgKey{{Room: itRoom, Seq: 1}})
	if err != nil || len(found) != 1 || found[0].Version != 1 || found[0].Text != "hello" {
		t.Fatalf("message after sweep = %+v, %v; want the edit applied", found, err)
	}
	pins, err := st.PinState(t.Context(), itRoom)
	if err != nil || pins.Version != 1 || len(pins.Pins) != 1 {
		t.Fatalf("pin state after sweep = %+v, %v; want v1 with one pin", pins, err)
	}
	for _, c := range store.AllStateCollections() {
		docs, _, err := st.EventStates().Pending(t.Context(), c, time.Time{}, time.Now(), store.PendingCursor{}, 10)
		if err != nil || len(docs) != 0 {
			t.Fatalf("pending %s after the sweep = %+v, %v; want none", c, docs, err)
		}
	}
	if s.Failures() != 0 {
		t.Fatalf("failures = %d, want 0", s.Failures())
	}
}
