package recovery_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
)

type call struct{ room, from uint64 }

type recorder struct {
	next    recovery.Rooms
	entered chan call
	done    chan call

	mu    sync.Mutex
	calls []call
	errs  map[uint64]error
	gates map[uint64]chan struct{}
}

func newRecorder(next recovery.Rooms) *recorder {
	return &recorder{
		next:    next,
		entered: make(chan call, 4096),
		done:    make(chan call, 4096),
		errs:    map[uint64]error{},
		gates:   map[uint64]chan struct{}{},
	}
}

func (r *recorder) Recover(ctx context.Context, room, from uint64) error {
	c := call{room: room, from: from}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	err, gate := r.errs[room], r.gates[room]
	r.mu.Unlock()
	signal(r.entered, c)
	defer signal(r.done, c)
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	if r.next != nil {
		return r.next.Recover(ctx, room, from)
	}
	return nil
}

func signal(ch chan call, c call) {
	select {
	case ch <- c:
	default:
	}
}

func (r *recorder) failFor(room uint64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs[room] = err
}

func (r *recorder) hold(room uint64) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	gate := make(chan struct{})
	r.gates[room] = gate
	return gate
}

func (r *recorder) list() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func (r *recorder) awaitDone(t *testing.T, room uint64) call {
	t.Helper()
	for {
		c := awaitSignal(t, "recovery of a room", r.done)
		if c.room == room {
			return c
		}
	}
}

type hookedTimeline struct {
	recovery.Timeline
	before func(room uint64) error
}

func (h hookedTimeline) Last(ctx context.Context, room, thread uint64) (seq, pts uint64, err error) {
	if err := h.before(room); err != nil {
		return 0, 0, err
	}
	return h.Timeline.Last(ctx, room, thread)
}
