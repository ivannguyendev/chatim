package projection_test

import (
	"context"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type armedCheck struct {
	key     store.MsgKey
	counter string
}

type fakeTimers struct {
	mu       sync.Mutex
	armed    []armedCheck
	disarmed []uint64
	err      error
}

func (f *fakeTimers) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (timer.Timer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return timer.Timer{}, f.err
	}
	f.armed = append(f.armed, armedCheck{key: key, counter: counter})
	return timer.Timer{Seq: uint64(len(f.armed))}, nil
}

func (f *fakeTimers) Disarm(_ context.Context, tm timer.Timer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disarmed = append(f.disarmed, tm.Seq)
}

func (f *fakeTimers) checks() ([]armedCheck, []uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.armed), slices.Clone(f.disarmed)
}

type fakeQueue struct {
	mu     sync.Mutex
	events []*chatimv1.Event
	err    error
}

func (q *fakeQueue) Enqueue(_ uint64, evs []*chatimv1.Event) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.events = append(q.events, evs...)
	return nil
}

func (q *fakeQueue) ids() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.events))
	for i, ev := range q.events {
		out[i] = ev.GetId()
	}
	return out
}

type failingReplyCount struct{ err error }

func (f failingReplyCount) AddReplyCount(context.Context, store.MsgKey, int) (domain.ReplyCount, error) {
	return domain.ReplyCount{}, f.err
}
