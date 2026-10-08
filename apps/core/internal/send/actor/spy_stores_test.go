package actor_test

import (
	"context"
	"errors"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
	inserted int
	findHook func(call int) error
}

func (m *spyMessages) Insert(ctx context.Context, msgs []domain.Message) []store.Result {
	m.mu.Lock()
	m.inserted += len(msgs)
	m.mu.Unlock()
	return m.Messages.Insert(ctx, msgs)
}

func (m *spyMessages) insertedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inserted
}

func (m *spyMessages) Last(ctx context.Context, room, thread uint64) (uint64, error) {
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
	mu         sync.Mutex
	members    int
	getErr     error
	memberErr  error
	memberHook func(context.Context)
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
	err, hook := r.memberErr, r.memberHook
	r.mu.Unlock()
	if err != nil {
		return domain.Member{}, err
	}
	m, err := r.Rooms.Member(ctx, room, user)
	if hook != nil {
		hook(ctx)
	}
	return m, err
}

func (r *spyRooms) setMemberHook(hook func(context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memberHook = hook
}

func (r *spyRooms) memberCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.members
}
