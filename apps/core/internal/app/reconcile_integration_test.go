package app

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

func TestRealInfraWorkersPublishWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	core.awaitTerm(t)

	inserted := time.Now()
	outside := domain.Message{
		Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core",
		CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	st := itStore(it, core)
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}
	awaitLiveIDs(t, live, inserted, pbconv.MessageEventID(room, 0, 1))
	if got := awaitActivity(t, st, room, 1); got.LastMsgAt.IsZero() || got.LastChangeAt.Before(got.LastMsgAt) {
		t.Fatalf("room activity = %+v, want last message and change times", got)
	}
}

func TestRealInfraRoomCreatedComesFromTheFastPathAndTheWorkers(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	live := subscribeLive(t, it, core.cfg, "*")

	began := time.Now()
	fast := createRoom(t, dialCore(t, core.cfg))
	awaitLiveIDs(t, live, began, pbconv.RoomCreatedEventID(parseRoom(t, fast)))

	core.awaitTerm(t)
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
	core := startCore(t, it, itFastEffects)
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
