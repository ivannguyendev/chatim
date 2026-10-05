package grpcsrv_test

import (
	"context"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
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

func newMutator(t *testing.T, rg *rig, o options) *mutate.Mutator {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events mutate.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	m, err := mutate.New(mutate.Deps{Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: events, Now: o.now})
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}
