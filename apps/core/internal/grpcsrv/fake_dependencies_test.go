package grpcsrv_test

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type acceptAllCIDs struct{}

func (acceptAllCIDs) Reserve(_ context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error) {
	out := make([]dedupe.Verdict, len(keys))
	for i := range out {
		out[i] = dedupe.Verdict{Status: dedupe.Reserved}
	}
	return out, nil
}

func (acceptAllCIDs) Commit(context.Context, []dedupe.Entry) error { return nil }

func (acceptAllCIDs) Abort(context.Context, []dedupe.Key) error { return nil }

type nopPublisher struct{}

func (nopPublisher) Enqueue(uint64, []*chatimv1.Event) error { return nil }

func (nopPublisher) Skip(uint64, []uint64) error { return nil }

type nopMarker struct{}

func (nopMarker) Mark(context.Context, uint64, uint64) error { return nil }

type fakeSender struct {
	mu   sync.Mutex
	cmds []actor.SendCmd
	ack  actor.Ack
	err  error
}

func (f *fakeSender) Send(_ context.Context, c actor.SendCmd) (actor.Ack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, c)
	return f.ack, f.err
}

func (f *fakeSender) sent() []actor.SendCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]actor.SendCmd(nil), f.cmds...)
}
