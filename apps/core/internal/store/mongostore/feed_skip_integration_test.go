package mongostore

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestFeedSkipsSummaryPinActivityEditHideAndClearWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
	ctx := t.Context()
	room := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
	owner := domain.Member{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}
	if err := s.Create(ctx, room, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	seedTimeline(t, s, itRoom, 0, 1)
	cur, err := NewFeed(db).Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, want := range []store.ChangeKind{store.RoomInserted, store.MessageInserted} {
		if c, err := cur.Next(wait); err != nil || c.Kind != want {
			t.Fatalf("Next = %+v, %v; want kind %d", c, err, want)
		}
	}
	m := msgAt(itRoom, 0, 1)
	key := store.KeyOf(m)
	sum := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}
	if ok, err := s.SetReactions(ctx, key, 0, sum); err != nil || !ok {
		t.Fatalf("SetReactions = %v, %v", ok, err)
	}
	pins := domain.PinState{Pins: []domain.Pin{{Seq: 1, By: "alice", At: codecTime, PV: 1}}, Version: 1}
	if ok, err := s.ApplyPins(ctx, itRoom, 0, pins); err != nil || !ok {
		t.Fatalf("ApplyPins = %v, %v", ok, err)
	}
	if err := s.TouchActivity(ctx, []store.Activity{{Room: itRoom, Seq: 1, At: codecTime}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	edit := domain.Edit{Room: itRoom, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "sửa", Prev: m.Text, At: codecTime}
	if err := s.ApplyEdit(ctx, edit); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if err := s.Hidden().Hide(ctx, "bob", key, codecTime); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if _, err := s.ClearHistory(ctx, itRoom, "alice", codecTime); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, _, err := s.Reactions().Set(ctx, domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "👍", At: codecTime}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	c, err := cur.Next(wait)
	if err != nil || c.Kind != store.ReactionChanged || c.Reaction.User != "alice" || c.Reaction.N != 1 {
		t.Fatalf("next change = %+v, %v; want only the reaction after the summary, pin, activity, edit, hide and clear writes", c, err)
	}
}
