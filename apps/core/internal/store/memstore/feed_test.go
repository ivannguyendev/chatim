package memstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestFeedLosesHistoryUntilForgotten(t *testing.T) {
	feed := memstore.NewFeed(memstore.NewMessages(), nil)
	feed.LoseHistory()
	if _, err := feed.Open(t.Context()); !errors.Is(err, store.ErrFeedHistoryLost) {
		t.Fatalf("Open after lost history = %v, want ErrFeedHistoryLost", err)
	}
	if err := feed.Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := feed.Open(t.Context()); err != nil {
		t.Fatalf("Open after Forget: %v", err)
	}
}

func TestFeedRejectsForeignPositions(t *testing.T) {
	cur, err := memstore.NewFeed(memstore.NewMessages(), nil).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, pos := range []store.Position{nil, store.Position("x"), store.Position("-1")} {
		if err := cur.Confirm(t.Context(), pos); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Confirm(%q) = %v, want ErrInvalidArgument", pos, err)
		}
	}
}

func TestFeedWithoutRoomsSeesOnlyMessages(t *testing.T) {
	msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
	cur, err := memstore.NewFeed(msgs, nil).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	r := domain.Room{ID: 9, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: now, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: 9, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: now}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := domain.Message{Room: 9, Seq: 1, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: now}
	if res := msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert: %+v", res)
	}
	c, err := cur.Next(t.Context())
	if err != nil || c.Kind != store.MessageInserted || c.Msg.Seq != 1 {
		t.Fatalf("first change = %+v, %v; want the message, the room is not logged", c, err)
	}
}
