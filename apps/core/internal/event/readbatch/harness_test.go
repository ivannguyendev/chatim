package readbatch_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/readbatch"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const enqueueFailedMsg = "read positions enqueue failed; the sweeper republishes them"

var (
	errQueueFull = errors.New("publish queue full")
	window       = time.Second
	roomA        = domain.Room{ID: 101, Tenant: "acme", Type: domain.RoomGroup}
	roomB        = domain.Room{ID: 202, Tenant: "beta", Type: domain.RoomDM}
)

type call struct {
	room   uint64
	events []*chatimv1.Event
}

type fakePublisher struct {
	mu    sync.Mutex
	calls []call
	fail  error
}

func (f *fakePublisher) Enqueue(room uint64, events []*chatimv1.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{room: room, events: slices.Clone(events)})
	return f.fail
}

func (f *fakePublisher) setFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

func (f *fakePublisher) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakePublisher) byRoom() map[uint64][]*chatimv1.Event {
	out := map[uint64][]*chatimv1.Event{}
	for _, c := range f.snapshot() {
		out[c.room] = append(out[c.room], c.events...)
	}
	return out
}

type rig struct {
	*readbatch.Queue
	pub  *fakePublisher
	sink *testlog.Sink
	done chan error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	rg := &rig{pub: &fakePublisher{}, sink: &testlog.Sink{}, done: make(chan error, 1)}
	q, err := readbatch.New(rg.pub.Enqueue, readbatch.Config{Window: window}, rg.sink.Logger())
	if err != nil {
		t.Fatalf("readbatch.New: %v", err)
	}
	rg.Queue = q
	return rg
}

func started(t *testing.T) *rig {
	t.Helper()
	rg := newRig(t)
	go func() { rg.done <- rg.Run(context.Background()) }()
	synctest.Wait()
	return rg
}

func (rg *rig) closeAndWait(t *testing.T) {
	t.Helper()
	if err := rg.Close(context.Background()); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := <-rg.done; err != nil {
		t.Fatalf("Run after Close = %v", err)
	}
}

func (rg *rig) tick() {
	time.Sleep(window)
	synctest.Wait()
}

func (rg *rig) expectCounts(t *testing.T, entries, events, dropped, failures uint64) {
	t.Helper()
	got := []uint64{rg.Entries(), rg.Events(), rg.Dropped(), rg.EnqueueFailures()}
	if want := []uint64{entries, events, dropped, failures}; !slices.Equal(got, want) {
		t.Fatalf("entries/events/dropped/failures = %v, want %v", got, want)
	}
}

func pos(seq, ver uint64) domain.ReadPosition { return domain.ReadPosition{Seq: seq, Ver: ver} }

func entries(evs []*chatimv1.Event) []*chatimv1.ReadEntry {
	var out []*chatimv1.ReadEntry
	for _, ev := range evs {
		out = append(out, ev.GetReadPositions().GetEntries()...)
	}
	return out
}

func lines(evs []*chatimv1.Event) []string {
	var out []string
	for _, e := range entries(evs) {
		out = append(out, e.GetUser()+":"+strconv.FormatUint(e.GetReadSeq(), 10)+":"+strconv.FormatUint(e.GetReadVer(), 10))
	}
	return out
}

func user(i int) string { return "u" + strconv.Itoa(10_000+i) }
