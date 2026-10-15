package publish

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ackFollowQueue = 4096

type pendingAck struct {
	future jetstream.PubAckFuture
	key    store.MsgKey
	marked bool
	target store.StateTarget
	clear  bool
}

type ackFollower struct {
	queue    chan pendingAck
	marks    *ackMarks
	clears   StateClears
	full     atomic.Bool
	log      *slog.Logger
	counters *Counters
}

func (p *Publisher) newAckFollower() *ackFollower {
	if p.marks == nil && p.clears == nil {
		return nil
	}
	return &ackFollower{queue: make(chan pendingAck, ackFollowQueue), marks: p.marks, clears: p.clears, log: p.log, counters: p.counters}
}

func (a *ackFollower) follow(room uint64, ev *chatimv1.Event, f jetstream.PubAckFuture) {
	if a == nil {
		return
	}
	pending := pendingAck{future: f}
	if a.marks != nil {
		pending.key, pending.marked = markKey(room, ev)
	}
	if a.clears != nil {
		pending.target, pending.clear = StateTargetOf(ev)
	}
	if !pending.marked && !pending.clear {
		return
	}
	select {
	case a.queue <- pending:
		a.full.Store(false)
	default:
		a.drop(pending)
	}
}

func (a *ackFollower) drop(pending pendingAck) {
	if pending.marked {
		a.counters.markQueueFull.Add(1)
	}
	if pending.clear {
		a.counters.stateTrackFull.Add(1)
	}
	if a.full.CompareAndSwap(false, true) {
		a.log.Warn("ack follower queue full; events left unmarked or flagged are republished later")
	}
}

func (a *ackFollower) run(abort <-chan struct{}) {
	for {
		select {
		case pending, open := <-a.queue:
			if !open {
				return
			}
			if !acked(pending.future, abort) {
				continue
			}
			if pending.marked {
				a.marks.track(pending.key)
			}
			if pending.clear {
				a.clears.Add(pending.target)
			}
		case <-abort:
			return
		}
	}
}

func acked(f jetstream.PubAckFuture, abort <-chan struct{}) bool {
	select {
	case <-f.Ok():
		return true
	case <-f.Err():
		return false
	case <-abort:
		return false
	}
}

func (p *Publisher) startFollowers() (stop func()) {
	var marking, following sync.WaitGroup
	if p.marks != nil {
		marking.Go(func() { p.marks.run(p.abort) })
	}
	for _, s := range p.shards {
		if s.acks != nil {
			following.Go(func() { s.acks.run(p.abort) })
		}
	}
	return func() {
		for _, s := range p.shards {
			if s.acks != nil {
				close(s.acks.queue)
			}
		}
		following.Wait()
		if p.marks != nil {
			close(p.marks.queue)
		}
		marking.Wait()
	}
}
