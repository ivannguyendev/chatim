package reconcile

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type entry struct {
	pos     store.Position
	msg     *nats.Msg
	future  jetstream.PubAckFuture
	acked   bool
	retryAt time.Time
}

type window struct {
	js      publish.JetStream
	limit   int
	backoff time.Duration
	failed  func(error)
	entries []*entry
	settled store.Position
}

func newWindow(js publish.JetStream, limit int, backoff time.Duration, failed func(error)) *window {
	return &window{js: js, limit: limit, backoff: backoff, failed: failed}
}

func (w *window) done(pos store.Position) {
	w.entries = append(w.entries, &entry{pos: pos, acked: true})
}

func (w *window) send(pos store.Position, msg *nats.Msg) {
	e := &entry{pos: pos, msg: msg}
	w.publish(e)
	w.entries = append(w.entries, e)
}

func (w *window) publish(e *entry) {
	f, err := w.js.PublishMsgAsync(e.msg)
	if err != nil {
		w.retry(e, err)
		return
	}
	e.future = f
}

func (w *window) retry(e *entry, err error) {
	e.future, e.retryAt = nil, time.Now().Add(w.backoff)
	w.failed(err)
}

func (w *window) poll(e *entry) bool {
	switch {
	case e.acked:
		return true
	case e.future == nil:
		if !time.Now().Before(e.retryAt) {
			w.publish(e)
		}
		return false
	}
	select {
	case <-e.future.Ok():
		e.acked = true
	case err := <-e.future.Err():
		w.retry(e, err)
	default:
	}
	return e.acked
}

func (w *window) collect() {
	for len(w.entries) > 0 && w.poll(w.entries[0]) {
		w.settled = w.entries[0].pos
		w.entries[0] = nil
		w.entries = w.entries[1:]
	}
}

func (w *window) full() bool { return len(w.entries) >= w.limit }

func (w *window) drain(ctx context.Context) {
	for {
		w.collect()
		if len(w.entries) == 0 || w.awaitHead(ctx, nil, w.backoff) != nil {
			return
		}
	}
}

func (w *window) awaitHead(ctx context.Context, stop <-chan struct{}, limit time.Duration) error {
	head := w.entries[0]
	if head.future == nil {
		return sleep(ctx, stop, min(time.Until(head.retryAt), limit))
	}
	capped := time.NewTimer(limit)
	defer capped.Stop()
	select {
	case <-head.future.Ok():
		head.acked = true
	case err := <-head.future.Err():
		w.retry(head, err)
	case <-capped.C:
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
