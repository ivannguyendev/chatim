package sweep_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type stuckFuture struct {
	ok  chan *jetstream.PubAck
	err chan error
	msg *nats.Msg
}

func (f *stuckFuture) Ok() <-chan *jetstream.PubAck { return f.ok }

func (f *stuckFuture) Err() <-chan error { return f.err }

func (f *stuckFuture) Msg() *nats.Msg { return f.msg }

type oneStuckJetStream struct {
	stuck string
	mu    sync.Mutex
	acked int
}

func (j *oneStuckJetStream) PublishMsgAsync(m *nats.Msg, _ ...jetstream.PublishOpt) (jetstream.PubAckFuture, error) {
	f := &stuckFuture{ok: make(chan *jetstream.PubAck, 1), err: make(chan error, 1), msg: m}
	if m.Header.Get(jetstream.MsgIDHeader) != j.stuck {
		j.mu.Lock()
		j.acked++
		f.ok <- &jetstream.PubAck{Stream: "FAKE", Sequence: uint64(j.acked)}
		j.mu.Unlock()
	}
	return f, nil
}

func (j *oneStuckJetStream) PublishAsyncComplete() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

var _ publish.JetStream = (*oneStuckJetStream)(nil)

func TestAcksThatArrivedBeforeTheDeadlineAreNotCountedAsFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		js := &oneStuckJetStream{stuck: pbconv.MessageEventID(room, 0, 1)}
		s, err := sweep.New(sweep.Deps{States: rg.states, Checkpoints: rg.marks, Owner: rg.owner, JS: js, Rebuilders: rg.rebuilders}, rg.cfg, rg.sink.Logger())
		if err != nil {
			t.Fatalf("sweep.New: %v", err)
		}
		rg.Sweeper = s
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		for seq := uint64(1); seq <= 8; seq++ {
			rg.send(t, room, seq, old.Add(time.Duration(seq)*time.Millisecond))
		}
		rg.start()
		time.Sleep(setup.AckTimeout)
		synctest.Wait()
		docs := rg.pending(t, store.StateMessages)
		if len(docs) != 1 || docs[0].Seq != 1 {
			t.Fatalf("pending = %+v, want only the unacked seq 1", docs)
		}
		if rg.PublishFailures() != 1 || rg.Republished(store.StateMessages) != 7 {
			t.Fatalf("publish failures %d, republished %d; want 1 and 7", rg.PublishFailures(), rg.Republished(store.StateMessages))
		}
		rg.stop(t)
	})
}
