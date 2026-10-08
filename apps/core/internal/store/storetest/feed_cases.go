package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const feedWait = 10 * time.Second

type feedCase struct {
	name string
	run  func(t *testing.T, msgs store.Messages, rooms store.Rooms, feed store.ChangeFeed)
}

func RunFeed(t *testing.T, open func(t *testing.T) (store.Messages, store.Rooms, store.ChangeFeed)) {
	t.Helper()
	for _, c := range feedCases() {
		t.Run(c.name, func(t *testing.T) {
			msgs, rooms, feed := open(t)
			c.run(t, msgs, rooms, feed)
		})
	}
}

func feedCases() []feedCase {
	return []feedCase{
		{"new inserts come out in commit order with their content", feedOrder},
		{"room inserts and their creation members come out in commit order", feedRooms},
		{"inserts after bootstrap but before the first open are read", feedStartsAtBootstrap},
		{"reopen resumes after the confirmed position", feedResume},
		{"an older confirm does not move the position back", feedNoRewind},
		{"forget restarts from now", feedForget},
	}
}

func openCursor(t *testing.T, feed store.ChangeFeed) store.Cursor {
	t.Helper()
	cur, err := feed.Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	return cur
}

func closeCursor(t *testing.T, cur store.Cursor) {
	t.Helper()
	if err := cur.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func confirm(t *testing.T, cur store.Cursor, c store.Change) {
	t.Helper()
	if err := cur.Confirm(t.Context(), c.Position); err != nil {
		t.Fatalf("Confirm(seq %d): %v", c.Msg.Seq, err)
	}
}

func nextChanges(t *testing.T, cur store.Cursor, n int) []store.Change {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), feedWait)
	defer cancel()
	out := make([]store.Change, 0, n)
	for range n {
		c, err := cur.Next(ctx)
		if err != nil {
			t.Fatalf("Next after %d changes: %v", len(out), err)
		}
		if c.CommittedAt.IsZero() || len(c.Position) == 0 {
			t.Fatalf("change for seq %d has no commit time or position: %+v", c.Msg.Seq, c)
		}
		out = append(out, c)
	}
	return out
}

func messagesOf(cs []store.Change) []domain.Message {
	out := make([]domain.Message, len(cs))
	for i, c := range cs {
		out[i] = c.Msg
	}
	return out
}

func insertEach(t *testing.T, s store.Messages, msgs ...domain.Message) {
	t.Helper()
	for _, m := range msgs {
		mustInsert(t, s, []domain.Message{m})
	}
}

func feedOrder(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	want := []domain.Message{msg(roomA, mainThread, 1), msg(roomB, sideThread, 1), msg(roomA, mainThread, 2)}
	insertEach(t, msgs, want...)
	assertMessages(t, messagesOf(nextChanges(t, cur, len(want))), want)
}

func feedStartsAtBootstrap(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	early := msg(roomA, mainThread, 1)
	insertEach(t, msgs, early)
	cur := openCursor(t, feed)
	assertMessages(t, messagesOf(nextChanges(t, cur, 1)), []domain.Message{early})
	later := msg(roomA, mainThread, 2)
	insertEach(t, msgs, later)
	assertMessages(t, messagesOf(nextChanges(t, cur, 1)), []domain.Message{later})
}

func feedResume(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	all := span(roomA, mainThread, 1, 3)
	insertEach(t, msgs, all...)
	got := nextChanges(t, cur, 3)
	confirm(t, cur, got[1])
	closeCursor(t, cur)
	again := openCursor(t, feed)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), all[2:])
}

func feedNoRewind(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	all := span(roomA, mainThread, 1, 3)
	insertEach(t, msgs, all...)
	got := nextChanges(t, cur, 3)
	confirm(t, cur, got[1])
	confirm(t, cur, got[0])
	closeCursor(t, cur)
	again := openCursor(t, feed)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), all[2:])
}

func feedForget(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	insertEach(t, msgs, span(roomA, mainThread, 1, 2)...)
	got := nextChanges(t, cur, 2)
	confirm(t, cur, got[0])
	closeCursor(t, cur)
	if err := feed.Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	again := openCursor(t, feed)
	fresh := msg(roomA, mainThread, 3)
	insertEach(t, msgs, fresh)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), []domain.Message{fresh})
}
