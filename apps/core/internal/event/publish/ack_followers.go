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
	target store.StateTarget
}

type ackFollower struct {
	queue    chan pendingAck
	clears   StateClears
	full     atomic.Bool
	log      *slog.Logger
	counters *Counters
}

func (p *Publisher) newAckFollower() *ackFollower {
	if p.clears == nil {
		return nil
	}
	return &ackFollower{queue: make(chan pendingAck, ackFollowQueue), clears: p.clears, log: p.log, counters: p.counters}
}

func (a *ackFollower) follow(ev *chatimv1.Event, f jetstream.PubAckFuture) {
	if a == nil {
		return
	}
	target, ok := StateTargetOf(ev)
	if !ok {
		return
	}
	select {
	case a.queue <- pendingAck{future: f, target: target}:
		a.full.Store(false)
	default:
		a.counters.stateTrackFull.Add(1)
		if a.full.CompareAndSwap(false, true) {
			a.log.Warn("ack follower queue full; flagged events are republished by the sweeper")
		}
	}
}

func (a *ackFollower) run(abort <-chan struct{}) {
	for {
		select {
		case pending, open := <-a.queue:
			if !open {
				return
			}
			if acked(pending.future, abort) {
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
	var following sync.WaitGroup
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
	}
}
