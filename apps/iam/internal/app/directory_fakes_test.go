package app_test

import (
	"context"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

type outage struct {
	down      atomic.Bool
	pings     chan struct{}
	reloads   chan struct{}
	hangPing  atomic.Bool
	pingCount atomic.Int64
	pingsDone atomic.Int64
	loadGate  chan struct{}
	feedAsked chan int
	slotAsked chan struct{}
	seq       atomic.Int64
	lastFeed  atomic.Int64
	closedAt  atomic.Int64
}

func (f *fakeStore) TryLoadSlot(context.Context, int) (func(), bool, error) {
	if f.slotAsked != nil {
		select {
		case f.slotAsked <- struct{}{}:
		default:
		}
	}
	if f.down.Load() {
		return nil, false, errOpen
	}
	return func() {}, true, nil
}

func (f *fakeStore) LoadStart(context.Context) (store.Position, error) {
	if f.down.Load() {
		return store.Position{}, errOpen
	}
	return store.Position{}, nil
}

func (f *fakeStore) LoadPage(ctx context.Context, _ store.AccountKey, _ int) ([]store.DirectoryEntry, error) {
	if f.loadGate != nil {
		select {
		case <-f.loadGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.down.Load() {
		return nil, errOpen
	}
	return []store.DirectoryEntry{{TenantID: "shop", ID: "lan", DisplayName: "Lan", State: store.StateActive, Ver: 1}}, nil
}

func (f *fakeStore) Changes(_ context.Context, after store.Position, limit int) (store.ChangePage, error) {
	f.lastFeed.Store(f.seq.Add(1))
	if f.feedAsked != nil {
		select {
		case f.feedAsked <- limit:
		default:
		}
	}
	if f.down.Load() {
		return store.ChangePage{}, errOpen
	}
	return store.ChangePage{Next: after}, nil
}

func signal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

type idleListener struct{}

func openIdleListener(context.Context, string) (store.Listener, error) { return idleListener{}, nil }

func (idleListener) Wait(ctx context.Context) (store.Notice, error) {
	<-ctx.Done()
	return store.NoticeNone, ctx.Err()
}

func (idleListener) Close(context.Context) error { return nil }
