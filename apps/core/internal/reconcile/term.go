package reconcile

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errReaderStopped = errors.New("change feed reader stopped")

type term struct {
	r         *Reconciler
	cur       store.Cursor
	win       *window
	changes   chan store.Change
	failed    chan error
	next      *store.Change
	confirmed store.Position
	checked   time.Time
}

func newTerm(r *Reconciler, cur store.Cursor) *term {
	return &term{
		r: r, cur: cur,
		win:     newWindow(r.deps.JS, r.cfg.Window, r.cfg.Poll, r.republishFailed),
		changes: make(chan store.Change, r.cfg.Batch),
		failed:  make(chan error, 1),
		checked: time.Now(),
	}
}

func (t *term) run(ctx context.Context) error {
	readCtx, stopReading := context.WithCancel(ctx)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		t.read(readCtx)
	}()
	defer func() {
		stopReading()
		<-reading
	}()
	tick := time.NewTicker(t.r.cfg.ConfirmEvery)
	defer tick.Stop()
	for {
		if c := t.next; c != nil {
			t.next = nil
			if err := t.handle(ctx, *c); err != nil {
				return err
			}
			continue
		}
		select {
		case c, open := <-t.changes:
			if !open {
				return t.readFailure()
			}
			if err := t.handle(ctx, c); err != nil {
				return err
			}
		case <-tick.C:
			if err := t.checkpoint(ctx); err != nil {
				return err
			}
		case <-t.r.stop:
			return errStopped
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *term) read(ctx context.Context) {
	defer close(t.changes)
	for {
		c, err := t.cur.Next(ctx)
		switch {
		case errors.Is(err, store.ErrCorruptChange):
			t.r.drop(ctx, "dropping change that cannot become an event", err)
			continue
		case err != nil:
			t.failed <- err
			return
		}
		select {
		case t.changes <- c:
		case <-ctx.Done():
			return
		}
	}
}

func (t *term) readFailure() error {
	select {
	case err := <-t.failed:
		return err
	default:
		return errReaderStopped
	}
}

func (t *term) handle(ctx context.Context, first store.Change) error {
	if err := t.pause(ctx, first.CommittedAt.Add(t.r.cfg.Delay)); err != nil {
		return err
	}
	t.r.watchLag(ctx, first.CommittedAt)
	batch := t.gather(first)
	acked := t.r.acked(ctx, batch)
	for i, c := range batch {
		if acked[i] || c.Kind == store.RoomInserted {
			t.win.done(c.Position)
			continue
		}
		if err := t.publish(ctx, c); err != nil {
			return err
		}
	}
	t.win.collect()
	return nil
}

func (t *term) gather(first store.Change) []store.Change {
	batch := []store.Change{first}
	due := time.Now().Add(-t.r.cfg.Delay)
	for len(batch) < t.r.cfg.Batch {
		select {
		case c, open := <-t.changes:
			if !open {
				return batch
			}
			if c.CommittedAt.After(due) {
				t.next = &c
				return batch
			}
			batch = append(batch, c)
		default:
			return batch
		}
	}
	return batch
}

func (t *term) publish(ctx context.Context, c store.Change) error {
	msg, err := t.r.message(ctx, c)
	switch {
	case errors.Is(err, errUndeliverable):
		t.r.drop(ctx, "dropping change that cannot become an event", err)
		t.win.done(c.Position)
		return nil
	case err != nil:
		return err
	}
	if err := t.makeRoom(ctx); err != nil {
		return err
	}
	t.win.send(c.Position, msg)
	t.r.stats.republished.Add(1)
	return nil
}
