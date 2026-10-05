package memstore

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var _ store.ChangeFeed = (*Feed)(nil)

type Feed struct {
	msgs      *Messages
	mu        sync.Mutex
	confirmed int
	known     bool
	lost      bool
}

func NewFeed(msgs *Messages, rooms *Rooms, edits *Edits) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	if edits != nil {
		edits.attach(msgs)
	}
	return &Feed{msgs: msgs, confirmed: msgs.logLen(), known: true}
}

func (f *Feed) LoseHistory() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lost = true
}

func (f *Feed) Confirmed() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.confirmed, f.known
}

func (f *Feed) Open(ctx context.Context) (store.Cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lost {
		return nil, store.ErrFeedHistoryLost
	}
	next := f.confirmed
	if !f.known {
		next = f.msgs.logLen()
	}
	return &cursor{feed: f, next: next}, nil
}

func (f *Feed) Forget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmed, f.known, f.lost = 0, false, false
	return nil
}

func (f *Feed) confirm(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.known || n > f.confirmed {
		f.confirmed, f.known = n, true
	}
}

type cursor struct {
	feed *Feed
	next int
}

func (c *cursor) Next(ctx context.Context) (store.Change, error) {
	for {
		l, grew, ok := c.feed.msgs.logAt(c.next)
		if ok {
			c.next++
			return store.Change{Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
		}
		select {
		case <-grew:
		case <-ctx.Done():
			return store.Change{}, ctx.Err()
		}
	}
}

func (c *cursor) Confirm(ctx context.Context, pos store.Position) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := strconv.Atoi(string(pos))
	if err != nil || n < 0 {
		return fmt.Errorf("%w: memstore feed position %q", apperr.ErrInvalidArgument, pos)
	}
	c.feed.confirm(n)
	return nil
}

func (c *cursor) Close(context.Context) error { return nil }
