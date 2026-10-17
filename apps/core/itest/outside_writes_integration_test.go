package itest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itActivityPoll = 100 * time.Millisecond

const itSweepMargin = 1500 * time.Millisecond

func (c itCore) sweepBound() time.Duration {
	return c.cfg.Sweep.MinAge + c.cfg.Sweep.Interval + itSweepMargin
}

func expectSweptWithin(t *testing.T, c itCore, since time.Time, what string) {
	t.Helper()
	took := time.Since(since)
	if took > c.sweepBound() {
		t.Fatalf("%s swept after %v, want within SWEEP_MIN_AGE + SWEEP_INTERVAL + %v = %v", what, took, itSweepMargin, c.sweepBound())
	}
}

func flagged(t *testing.T, st *mongostore.Store, c store.StateCollection, room, seq uint64) bool {
	t.Helper()
	docs, _, err := st.EventStates().Pending(t.Context(), c, time.Unix(0, 0), time.Now().Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
	if err != nil {
		t.Fatalf("Pending(%s): %v", c, err)
	}
	for _, d := range docs {
		if d.Room == room && d.Seq == seq {
			return true
		}
	}
	return false
}

func awaitUnflagged(t *testing.T, st *mongostore.Store, c store.StateCollection, room, seq uint64) {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for flagged(t, st, c, room, seq) {
		if time.Now().After(deadline) {
			t.Fatalf("%s doc %d/%d still flagged after %v, want the flag cleared after the publish", c, room, seq, itLiveLimit)
		}
		time.Sleep(itActivityPoll)
	}
}

func itStore(it *itInfra, c itCore) *mongostore.Store {
	return mongostore.New(it.mongo.Database(c.cfg.MongoDB), mongostore.Options{})
}

func awaitActivity(t *testing.T, st *mongostore.Store, room, seq uint64) domain.Room {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for {
		r, err := st.Get(t.Context(), room)
		if err != nil {
			t.Fatalf("Get(%d): %v", room, err)
		}
		if r.LastSeq >= seq {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("room %d last seq = %d after %v, want %d", room, r.LastSeq, itLiveLimit, seq)
		}
		time.Sleep(itActivityPoll)
	}
}

func TestRealInfraSweeperPublishesWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastSweep)
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	core.awaitSweeper(t)

	inserted := time.Now()
	outside := domain.Message{
		Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core",
		CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	st := itStore(it, core)
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}
	if !flagged(t, st, store.StateMessages, room, 1) {
		t.Fatalf("message written outside the core is not flagged, want it pending until a publish")
	}
	awaitLiveIDs(t, live, inserted, pbconv.MessageEventID(room, 0, 1))
	expectSweptWithin(t, core, inserted, "msg_created of a message no core published")
	awaitUnflagged(t, st, store.StateMessages, room, 1)
	if got := awaitActivity(t, st, room, 1); got.LastMsgAt.IsZero() || got.LastChangeAt.Before(got.LastMsgAt) {
		t.Fatalf("room activity = %+v, want last message and change times", got)
	}
}

func TestRealInfraRoomCreatedComesFromTheFastPathAndTheSweeper(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastSweep)
	live := subscribeLive(t, it, core.cfg, "*")

	began := time.Now()
	fast := createRoom(t, dialCore(t, core.cfg))
	awaitLiveIDs(t, live, began, pbconv.RoomCreatedEventID(parseRoom(t, fast)))

	core.awaitSweeper(t)
	created := time.Now().UTC().Truncate(time.Millisecond)
	outside := domain.Room{ID: ids.NewRoomID(), Tenant: itTenant, Type: domain.RoomGroup, Name: "outside", CreatedBy: "migrator", CreatedAt: created, MemberCount: 1}
	owner := domain.Member{Room: outside.ID, Tenant: itTenant, User: "migrator", Role: domain.RoleOwner, JoinedAt: created}
	if err := itStore(it, core).Create(t.Context(), outside, []domain.Member{owner}); err != nil {
		t.Fatalf("create a room outside the core: %v", err)
	}
	awaitLiveIDs(t, live, time.Now(), pbconv.RoomCreatedEventID(outside.ID))
}

func TestRealInfraSendMessageMovesRoomActivity(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastSweep)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	resp, err := sendRetrying(caller(t.Context()), client, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "activity-1", Text: "moves the room"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := awaitActivity(t, itStore(it, core), room, resp.GetSeq())
	if got.LastSeq != resp.GetSeq() || got.LastMsgAt.IsZero() || got.LastChangeAt.IsZero() {
		t.Fatalf("room activity = %+v, want last seq %d with message and change times", got, resp.GetSeq())
	}
}
