package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errPublishFailed = errors.New("work record publish failed")

type entry struct {
	pos    store.Position
	future jetstream.PubAckFuture
	acked  bool
	failed bool
}

type window struct {
	js      publish.JetStream
	limit   int
	wait    time.Duration
	entries []*entry
	settled store.Position
	err     error
}

func newWindow(js publish.JetStream, limit int, wait time.Duration) *window {
	return &window{js: js, limit: limit, wait: wait}
}

func (w *window) done(pos store.Position) {
	w.entries = append(w.entries, &entry{pos: pos, acked: true})
}

func (w *window) send(pos store.Position, msg *nats.Msg) error {
	f, err := w.js.PublishMsgAsync(msg)
	if err != nil {
		return w.fail(err)
	}
	w.entries = append(w.entries, &entry{pos: pos, future: f})
	return nil
}

func (w *window) fail(err error) error {
	if w.err == nil {
		w.err = fmt.Errorf("%w: %w", errPublishFailed, err)
	}
	return w.err
}

func (w *window) poll(e *entry) bool {
	if e.acked || e.failed {
		return e.acked
	}
	select {
	case <-e.future.Ok():
		e.acked = true
	case err := <-e.future.Err():
		e.failed = true
		_ = w.fail(err)
	default:
	}
	return e.acked
}

func (w *window) collect() error {
	for len(w.entries) > 0 && w.poll(w.entries[0]) {
		w.settled = w.entries[0].pos
		w.entries[0] = nil
		w.entries = w.entries[1:]
	}
	return w.err
}

func (w *window) full() bool { return len(w.entries) >= w.limit }

func (w *window) drain(ctx context.Context) {
	for {
		_ = w.collect()
		if len(w.entries) == 0 || w.entries[0].failed || w.awaitHead(ctx, nil, w.wait) != nil {
			return
		}
	}
}

func (w *window) awaitHead(ctx context.Context, stop <-chan struct{}, limit time.Duration) error {
	head := w.entries[0]
	if head.failed {
		return w.err
	}
	capped := time.NewTimer(limit)
	defer capped.Stop()
	select {
	case <-head.future.Ok():
		head.acked = true
	case err := <-head.future.Err():
		head.failed = true
		return w.fail(err)
	case <-capped.C:
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
