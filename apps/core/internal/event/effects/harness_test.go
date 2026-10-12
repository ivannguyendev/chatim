package effects_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work/worktest"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	delay = 5 * time.Second
	retry = 3 * time.Second
	wait  = time.Second
	tick  = time.Second

	fetchFailedMsg = "work fetch failed; retrying"
)

var (
	errBoom = errors.New("boom")
	setup   = effects.Config{Partitions: 4, FetchBatch: 8, FetchWait: wait, RetryDelay: retry, Drain: tick, Poll: tick}
)

type owner struct {
	mu    sync.Mutex
	slots map[uint16]bool
}

func (o *owner) Owns(slot uint16) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.slots[slot]
}

func (o *owner) set(slot uint16, owned bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.slots[slot] = owned
}

type journal struct {
	mu      sync.Mutex
	entries []string
}

func (j *journal) add(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, s)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

type recorder struct {
	name    string
	journal *journal
	block   chan struct{}
	mu      sync.Mutex
	calls   [][]work.Record
	at      []time.Time
	fail    map[uint64]bool
}

func (r *recorder) effect(d time.Duration) effects.Effect {
	return effects.Effect{Name: r.name, Delay: d, Run: r.run}
}

func (r *recorder) run(ctx context.Context, recs []work.Record) []error {
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
		}
	}
	if r.journal != nil {
		r.journal.add(r.name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(recs))
	r.at = append(r.at, time.Now())
	errs := make([]error, len(recs))
	for i, rec := range recs {
		if r.fail[rec.Seq] {
			errs[i] = errBoom
		}
	}
	return errs
}

func (r *recorder) failSeqs(seqs ...uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = map[uint64]bool{}
	for _, s := range seqs {
		r.fail[s] = true
	}
}

func (r *recorder) record() ([][]work.Record, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls), slices.Clone(r.at)
}

type rig struct {
	*effects.Workers
	broker *worktest.Broker
	owner  *owner
	msgs   *recorder
	rooms  *recorder
	sink   *testlog.Sink
}

func newRig(t *testing.T, tune func(rg *rig, d *effects.Deps)) *rig {
	t.Helper()
	rg := &rig{
		broker: &worktest.Broker{}, owner: &owner{slots: map[uint16]bool{0: true}},
		msgs: &recorder{name: "msg"}, rooms: &recorder{name: "room"}, sink: &testlog.Sink{},
	}
	deps := effects.Deps{
		Queue: rg.broker.Queue,
		Owner: rg.owner,
		Registry: effects.Registry{
			store.MessageInserted: {rg.msgs.effect(delay)},
			store.RoomInserted:    {rg.rooms.effect(0)},
		},
	}
	if tune != nil {
		tune(rg, &deps)
	}
	w, err := effects.New(deps, setup, rg.sink.Logger())
	if err != nil {
		t.Fatalf("effects.New: %v", err)
	}
	rg.Workers = w
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rg.Run(ctx) }()
	t.Cleanup(func() {
		stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopped()
		if err := rg.Close(stop); err != nil {
			t.Errorf("Close: %v", err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v, want nil after Close", err)
		}
	})
	return rg
}

func msg(room, seq uint64, at time.Time) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: room, Seq: seq, CommittedAt: at}
}

func roomRec(room uint64, at time.Time) work.Record {
	return work.Record{Kind: store.RoomInserted, Room: room, CommittedAt: at}
}

func seqs(recs []work.Record) []uint64 {
	out := make([]uint64, len(recs))
	for i, r := range recs {
		out[i] = r.Seq
	}
	return out
}

func rooms(recs []work.Record) []uint64 {
	out := make([]uint64, len(recs))
	for i, r := range recs {
		out[i] = r.Room
	}
	return out
}
