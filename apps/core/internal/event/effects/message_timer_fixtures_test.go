package effects_test

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type armedCheck struct {
	key     store.MsgKey
	counter string
}

type fakeMessageTimers struct {
	mu    sync.Mutex
	armed []armedCheck
	err   error
}

func (f *fakeMessageTimers) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (timer.Timer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return timer.Timer{}, f.err
	}
	f.armed = append(f.armed, armedCheck{key: key, counter: counter})
	return timer.Timer{Seq: uint64(len(f.armed))}, nil
}

func (f *fakeMessageTimers) checks() []armedCheck {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]armedCheck(nil), f.armed...)
}
