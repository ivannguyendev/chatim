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
	markTimeout = time.Second
)

type Marker interface {
	Mark(ctx context.Context, keys []store.MsgKey) error
}

type Option func(*Publisher)

func WithAckMarks(m Marker) Option {
	return func(p *Publisher) {
		if m != nil {
			p.marks = &ackMarks{marker: m, queue: make(chan tracked, markQueue), fails: failureLog{log: p.log}, log: p.log}
		}
	}
}

type tracked struct {
	key    store.MsgKey
	future jetstream.PubAckFuture
}

type ackMarks struct {
	marker Marker
	queue  chan tracked
	full   atomic.Bool
	fails  failureLog
	log    *slog.Logger
}

func (a *ackMarks) track(key store.MsgKey, f jetstream.PubAckFuture) {
	if a == nil {
		return
	}
	select {
	case a.queue <- tracked{key: key, future: f}:
		a.full.Store(false)
	default:
		if a.full.CompareAndSwap(false, true) {
			a.log.Warn("ack mark queue full; reconciliation republishes unmarked events")
		}
	}
}

func (a *ackMarks) run(abort <-chan struct{}) {
	var batch []store.MsgKey
	for {
		var t tracked
		var open bool
		if len(batch) == 0 {
			select {
			case t, open = <-a.queue:
			case <-abort:
				return
			}
		} else {
			select {
			case t, open = <-a.queue:
			default:
				a.flush(batch)
				batch = batch[:0]
				continue
			}
		}
		if !open {
			a.flush(batch)
			return
		}
		if acked(t.future, abort) {
			batch = append(batch, t.key)
		}
		if len(batch) >= markBatch {
			a.flush(batch)
			batch = batch[:0]
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
		a.fails.record("marking acked events failed; reconciliation republishes them", "", err)
	}
}
