package grpcsrv_test

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
)

type countingLister struct {
	store.Mentions
	mu     sync.Mutex
	shared [][]uint64
	err    error
}

func (c *countingLister) ListShared(ctx context.Context, q store.SharedMentionQuery) ([]domain.Mention, error) {
	rooms := make([]uint64, len(q.Rooms))
	for i, r := range q.Rooms {
		rooms[i] = r.Room
	}
	c.mu.Lock()
	c.shared = append(c.shared, rooms)
	err := c.err
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return c.Mentions.ListShared(ctx, q)
}

func (c *countingLister) calls() [][]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.shared
	c.shared = nil
	return out
}
