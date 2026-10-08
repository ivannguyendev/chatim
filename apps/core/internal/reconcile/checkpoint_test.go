package reconcile_test

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type corruptFeed struct {
	store.ChangeFeed
	corrupt atomic.Int32
}

func (f *corruptFeed) Open(ctx context.Context) (store.Cursor, error) {
	cur, err := f.ChangeFeed.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &corruptCursor{Cursor: cur, feed: f}, nil
}

type corruptCursor struct {
	store.Cursor
	feed *corruptFeed
}

func (c *corruptCursor) Next(ctx context.Context) (store.Change, error) {
	ch, err := c.Cursor.Next(ctx)
	if err == nil && c.feed.corrupt.Add(-1) >= 0 {
		return store.Change{}, store.ErrCorruptChange
	}
	return ch, err
}

type kindlessFeed struct {
	store.ChangeFeed
	strip atomic.Int32
}

func (f *kindlessFeed) Open(ctx context.Context) (store.Cursor, error) {
	cur, err := f.ChangeFeed.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &kindlessCursor{Cursor: cur, feed: f}, nil
}

type kindlessCursor struct {
	store.Cursor
	feed *kindlessFeed
}

func (c *kindlessCursor) Next(ctx context.Context) (store.Change, error) {
	ch, err := c.Cursor.Next(ctx)
	if err == nil && c.feed.strip.Add(-1) >= 0 {
		ch.Kind = 0
	}
	return ch, err
}

func TestLostLeadershipWhileTheWindowIsFullEndsTheTerm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed before the window fills = %d, want 1", got)
		}
		rg.js.Hold()
		rg.insert(t, room, 2, 3, 4, 5, 6, 7)
		time.Sleep(tick)
		synctest.Wait()
		inFlight := 1 + setup.Window
		if got := len(rg.js.Attempts()); got != inFlight {
			t.Fatalf("attempts with a full window = %d, want %d", got, inFlight)
		}
		rg.owner.leading.Store(false)
		time.Sleep(3 * tick)
		synctest.Wait()
		rg.js.Release()
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != inFlight {
			t.Fatalf("attempts after losing the lead = %d, want %d", got, inFlight)
		}
		rg.owner.leading.Store(true)
		time.Sleep(3 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2, 3, 4, 5, 6, 7)) {
			t.Fatalf("stored after regaining the lead = %v, want seq 1..7", got)
		}
		if got := rg.confirmed(t); got != 7 {
			t.Fatalf("confirmed after regaining the lead = %d, want 7", got)
		}
	})
}

func TestCorruptChangeIsDroppedAndTheNextForwarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		corrupt := &corruptFeed{}
		corrupt.corrupt.Store(1)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			corrupt.ChangeFeed = f
			return corrupt
		}).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(2)) {
			t.Fatalf("stored = %v, want only %s", got, recordID(2))
		}
		if d := rg.Stats().Dropped; d != 1 || rg.sink.Count(dropMsg) != 1 {
			t.Fatalf("dropped = %d, logged %d; want 1 and 1", d, rg.sink.Count(dropMsg))
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}

func TestChangeOfUnknownKindIsDroppedAndConfirmedPast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kindless := &kindlessFeed{}
		kindless.strip.Store(1)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			kindless.ChangeFeed = f
			return kindless
		}).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(2)) {
			t.Fatalf("stored = %v, want only %s", got, recordID(2))
		}
		if d := rg.Stats().Dropped; d != 1 {
			t.Fatalf("dropped = %d, want 1", d)
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}
