package actor_test

import (
	"context"
	"errors"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var errHidden = errors.New("not visible yet")

type spyMessages struct {
	*memstore.Messages
	mu       sync.Mutex
	lasts    int
	pages    int
	finds    int
	findHook func(call int) error
}

func (m *spyMessages) Last(ctx context.Context, room, thread uint64) (seq, pts uint64, err error) {
	m.mu.Lock()
	m.lasts++
	m.mu.Unlock()
	return m.Messages.Last(ctx, room, thread)
}

func (m *spyMessages) Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error) {
	m.mu.Lock()
	m.pages++
	m.mu.Unlock()
	return m.Messages.Page(ctx, q)
}

func (m *spyMessages) Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	m.mu.Lock()
	m.finds++
	call, hook := m.finds, m.findHook
	m.mu.Unlock()
	if hook != nil {
		err := hook(call)
		switch {
		case errors.Is(err, errHidden):
			return nil, ctx.Err()
		case err != nil:
			return nil, err
		}
	}
	return m.Messages.Find(ctx, room, keys)
}

func (m *spyMessages) counts() (lasts, pages, finds int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lasts, m.pages, m.finds
}

type spyRooms struct {
	*memstore.Rooms
	mu        sync.Mutex
	members   int
	getErr    error
	memberErr error
}

func (r *spyRooms) Get(ctx context.Context, id uint64) (domain.Room, error) {
	r.mu.Lock()
	err := r.getErr
	r.mu.Unlock()
	if err != nil {
		return domain.Room{}, err
	}
	return r.Rooms.Get(ctx, id)
}

func (r *spyRooms) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	r.mu.Lock()
	r.members++
	err := r.memberErr
	r.mu.Unlock()
	if err != nil {
		return domain.Member{}, err
	}
	return r.Rooms.Member(ctx, room, user)
}

func (r *spyRooms) memberCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.members
}
