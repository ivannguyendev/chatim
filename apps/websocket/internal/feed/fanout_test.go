package feed

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type recorder struct {
	mu      sync.Mutex
	events  map[string][]uint64
	publish []Envelope
	jumps   []Jump
	log     []string
	gate    map[string]chan struct{}
}

func newRecorder() *recorder {
	return &recorder{events: map[string][]uint64{}, gate: map[string]chan struct{}{}}
}

func (r *recorder) block(room string) chan struct{} {
	ch := make(chan struct{})
	r.gate[room] = ch
	return ch
}

func (r *recorder) Event(e Envelope) {
	if ch, ok := r.gate[e.Room]; ok {
		<-ch
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[e.Room] = append(r.events[e.Room], e.Seq)
	r.log = append(r.log, "event "+strconv.FormatUint(e.Seq, 10))
}

func (r *recorder) Jumped(j Jump) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jumps = append(r.jumps, j)
	r.log = append(r.log, "jumped "+strconv.FormatUint(j.Seq, 10))
}

func (r *recorder) history() ([]Jump, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.jumps), slices.Clone(r.log)
}

func (r *recorder) Publish(e Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publish = append(r.publish, e)
}

func (r *recorder) seqs(room string) []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events[room])
}

func roomsOnDistinctWorkers(t *testing.T, workers int) (string, string) {
	t.Helper()
	first := "1"
	for i := 2; i < 1000; i++ {
		if other := strconv.Itoa(i); spread(other, workers) != spread(first, workers) {
			return first, other
		}
	}
	t.Fatal("no two rooms on distinct workers")
	return "", ""
}

func TestFanoutKeepsRoomOrderAndRunsOtherRoomsInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		slow, fast := roomsOnDistinctWorkers(t, 4)
		release := rec.block(slow)
		f := newFanout(4, 16, rec, quietLog())
		for seq := uint64(1); seq <= 5; seq++ {
			f.send(slow, job{env: Envelope{Room: slow, Seq: seq}})
			f.send(fast, job{env: Envelope{Room: fast, Seq: seq}})
		}
		synctest.Wait()
		if got := rec.seqs(fast); !slices.Equal(got, []uint64{1, 2, 3, 4, 5}) {
			t.Fatalf("fast room = %v while the slow room is blocked, want 1..5", got)
		}
		if got := rec.seqs(slow); len(got) != 0 {
			t.Fatalf("slow room = %v, want nothing yet", got)
		}
		close(release)
		if err := f.close(t.Context()); err != nil {
			t.Fatalf("close: %v", err)
		}
		if got := rec.seqs(slow); !slices.Equal(got, []uint64{1, 2, 3, 4, 5}) {
			t.Fatalf("slow room = %v, want 1..5 in order", got)
		}
	})
}

func TestFanoutSendWaitsWhileTheWorkerQueueIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		release := rec.block("9")
		f := newFanout(1, 1, rec, quietLog())
		sent := make(chan int, 4)
		go func() {
			for seq := uint64(1); seq <= 3; seq++ {
				f.send("9", job{env: Envelope{Room: "9", Seq: seq}})
				sent <- int(seq)
			}
		}()
		synctest.Wait()
		if len(sent) != 2 || f.depth() != 1 {
			t.Fatalf("sent %d with depth %d, want the third send waiting behind one queued job", len(sent), f.depth())
		}
		close(release)
		synctest.Wait()
		if len(sent) != 3 {
			t.Fatalf("sent %d after the worker freed up, want 3", len(sent))
		}
		if err := f.close(t.Context()); err != nil || !slices.Equal(rec.seqs("9"), []uint64{1, 2, 3}) {
			t.Fatalf("close = %v, delivered %v", err, rec.seqs("9"))
		}
	})
}

func TestFanoutRoutesPublishPacketsToPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		f := newFanout(2, 4, rec, quietLog())
		f.send("n1-p0-abc", job{publish: true, env: Envelope{Tenant: "acme", ID: "n1-p0-abc"}})
		if err := f.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(rec.publish) != 1 || rec.publish[0].ID != "n1-p0-abc" || len(rec.events) != 0 {
			t.Fatalf("publish %v events %v", rec.publish, rec.events)
		}
	})
}

func TestFanoutHaltReleasesABlockedSenderAndRefusesLaterSends(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		release := rec.block("9")
		f := newFanout(1, 1, rec, quietLog())
		f.send("9", job{env: Envelope{Room: "9", Seq: 1}})
		synctest.Wait()
		f.send("9", job{env: Envelope{Room: "9", Seq: 2}})
		result := make(chan bool, 1)
		go func() { result <- f.send("9", job{env: Envelope{Room: "9", Seq: 3}}) }()
		synctest.Wait()
		f.halt()
		if <-result {
			t.Fatal("a blocked send succeeded after halt")
		}
		close(release)
		if err := f.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if f.send("9", job{env: Envelope{Room: "9", Seq: 4}}) {
			t.Fatal("send after close succeeded")
		}
		if got := rec.seqs("9"); !slices.Equal(got, []uint64{1, 2}) {
			t.Fatalf("delivered %v, want the queued jobs 1 and 2", got)
		}
	})
}

func TestFanoutCloseGivesUpOnAStuckWorkerAndSkipsTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		release := rec.block("9")
		f := newFanout(1, 4, rec, quietLog())
		for seq := uint64(1); seq <= 3; seq++ {
			f.send("9", job{env: Envelope{Room: "9", Seq: seq}})
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := f.close(ctx); !errors.Is(err, errFanoutStuck) {
			t.Fatalf("close = %v, want stuck", err)
		}
		close(release)
		synctest.Wait()
		if got := rec.seqs("9"); !slices.Equal(got, []uint64{1}) {
			t.Fatalf("delivered %v after giving up, want only the job in hand", got)
		}
	})
}

func TestFanoutRecoversFromAHandlerPanicAndGoesOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		h := handlerFuncs{event: func(e Envelope) {
			if e.Seq == 2 {
				panic("bad event")
			}
			rec.Event(e)
		}}
		f := newFanout(1, 4, h, quietLog())
		for seq := uint64(1); seq <= 3; seq++ {
			f.send("9", job{env: Envelope{Room: "9", Seq: seq}})
		}
		if err := f.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := rec.seqs("9"); !slices.Equal(got, []uint64{1, 3}) || f.panics.Load() != 1 {
			t.Fatalf("delivered %v with %d panics, want 1 and 3 with one panic", got, f.panics.Load())
		}
	})
}
