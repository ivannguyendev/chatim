package publish

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	markQueue   = 4096
	markBatch   = 256
	markWindow  = 10 * time.Millisecond
	markTimeout = time.Second
)

type Marker interface {
	Mark(ctx context.Context, keys []store.MsgKey) error
}

type Option func(*Publisher)

func WithAckMarks(m Marker) Option {
	return func(p *Publisher) {
		if m != nil {
			p.marks = &ackMarks{marker: m, queue: make(chan tracked, markQueue), fails: failureLog{log: p.log}, log: p.log, counters: &Counters{}}
		}
	}
}

type tracked struct {
	key    store.MsgKey
	future jetstream.PubAckFuture
}

type ackMarks struct {
	marker   Marker
	queue    chan tracked
	full     atomic.Bool
	fails    failureLog
	log      *slog.Logger
	counters *Counters
}

func (a *ackMarks) track(key store.MsgKey, f jetstream.PubAckFuture) {
	if a == nil {
		return
	}
	select {
	case a.queue <- tracked{key: key, future: f}:
		a.full.Store(false)
	default:
		a.counters.markQueueFull.Add(1)
		if a.full.CompareAndSwap(false, true) {
			a.log.Warn("ack mark queue full; reconciliation republishes unmarked events")
		}
	}
}

func (a *ackMarks) run(abort <-chan struct{}) {
	window := time.NewTimer(markWindow)
	window.Stop()
	defer window.Stop()
	batch := make([]store.MsgKey, 0, markBatch)
	for {
		select {
		case t, open := <-a.queue:
			if !open {
				a.flush(batch)
				return
			}
			if !acked(t.future, abort) {
				continue
			}
			if len(batch) == 0 {
				window.Reset(markWindow)
			}
			batch = append(batch, t.key)
			if len(batch) >= markBatch {
				window.Stop()
				a.flush(batch)
				batch = batch[:0]
			}
		case <-window.C:
			a.flush(batch)
			batch = batch[:0]
		case <-abort:
			a.flush(batch)
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

func (a *ackMarks) flush(keys []store.MsgKey) {
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), markTimeout)
	defer cancel()
	if err := a.marker.Mark(ctx, keys); err != nil {
		a.counters.markFailed.Add(int64(len(keys)))
		a.fails.record("marking acked events failed; reconciliation republishes them", "", err)
	}
}
