package memstore

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var _ store.ChangeFeed = (*Feed)(nil)

type logged struct {
	msg domain.Message
	at  time.Time
}

func (s *Messages) appendLog(m domain.Message) {
	s.log = append(s.log, logged{msg: m, at: time.Now()})
	close(s.grew)
	s.grew = make(chan struct{})
}

func (s *Messages) logAt(i int) (logged, <-chan struct{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i < len(s.log) {
		return s.log[i], nil, true
	}
	return logged{}, s.grew, false
}

func (s *Messages) logLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.log)
}

type Feed struct {
	msgs      *Messages
	mu        sync.Mutex
	confirmed int
	known     bool
	lost      bool
}

func NewFeed(msgs *Messages) *Feed { return &Feed{msgs: msgs} }

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
			return store.Change{Msg: l.msg, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
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
