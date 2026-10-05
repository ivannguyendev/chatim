package reconcile_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestRoomChangesAreConfirmedWithoutAnEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
		feed := memstore.NewFeed(msgs, rooms)
		rg := newRig(t, func(store.ChangeFeed) store.ChangeFeed { return feed }).start(t)
		synctest.Wait()
		now := time.Now()
		other := domain.Room{ID: 777, Tenant: tenant, Type: domain.RoomGroup, Name: "other", CreatedBy: "alice", CreatedAt: now, MemberCount: 1}
		if err := rooms.Create(t.Context(), other, []domain.Member{{Room: 777, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: now}}); err != nil {
			t.Fatalf("create room: %v", err)
		}
		m := domain.Message{Room: room, Seq: 1, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: now.UTC()}
		if res := msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert: %+v", res)
		}
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("attempts = %v, want only %s", got, eventID(1))
		}
		if n, _ := feed.Confirmed(); n != 2 {
			t.Fatalf("confirmed = %d, want 2 (the room change and the message)", n)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0 for a room change", got)
		}
	})
}
