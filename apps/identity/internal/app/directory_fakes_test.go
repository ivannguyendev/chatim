package app_test

import (
	"context"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
)

var _ directory.Source = (*mongostore.Store)(nil)

type outage struct {
	down       atomic.Bool
	loadDown   atomic.Bool
	pings      chan struct{}
	reloads    chan struct{}
	hangPing   atomic.Bool
	pingCount  atomic.Int64
	pingsDone  atomic.Int64
	loadGate   chan struct{}
	slotAsked  chan struct{}
	streamOpen chan struct{}
	events     chan store.StreamEvent
	seq        atomic.Int64
	closedAt   atomic.Int64
}

type heldSlot struct{}

func (heldSlot) Renew(context.Context) (bool, error) { return true, nil }

func (heldSlot) Release() {}

func (f *fakeStore) TryLoadSlot(context.Context, string, int) (store.Slot, bool, error) {
	signal(f.slotAsked)
	if f.down.Load() || f.loadDown.Load() {
		return nil, false, errOpen
	}
	return heldSlot{}, true, nil
}

func (f *fakeStore) StreamStart(context.Context) (store.StreamToken, error) {
	if f.down.Load() {
		return nil, errOpen
	}
	return store.StreamToken("t0"), nil
}

func (f *fakeStore) LoadPage(ctx context.Context, _ store.UserKey, _ int) ([]store.DirectoryEntry, error) {
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
	return []store.DirectoryEntry{{TenantID: "shop", ID: "lan", DisplayName: "Lan", State: store.StateActive, Ver: 1, Realm: shopIssuer, Sub: "lan"}}, nil
}

func (f *fakeStore) OpenStream(context.Context, store.StreamToken) (store.ChangeStream, error) {
	signal(f.streamOpen)
	if f.down.Load() {
		return nil, errOpen
	}
	return eventStream{events: f.events}, nil
}

type eventStream struct {
	events chan store.StreamEvent
}

func (s eventStream) Next(ctx context.Context) (store.StreamEvent, bool, error) {
	select {
	case <-ctx.Done():
		return store.StreamEvent{}, false, ctx.Err()
	case ev := <-s.events:
		return ev, true, nil
	}
}

func (eventStream) Close(context.Context) error { return nil }

func signal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
