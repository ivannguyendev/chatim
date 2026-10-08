package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errReaderStopped = errors.New("change feed reader stopped")

type term struct {
	r         *Reconciler
	cur       store.Cursor
	win       *window
	changes   chan store.Change
	failed    chan error
	confirmed store.Position
	checked   time.Time
}

func newTerm(r *Reconciler, cur store.Cursor) *term {
	return &term{
		r: r, cur: cur,
		win:     newWindow(r.deps.JS, r.cfg.Window, r.cfg.Poll, r.publishFailed),
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
		select {
		case c, open := <-t.changes:
			if !open {
				return t.readFailure()
			}
			if err := t.forward(ctx, c); err != nil {
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
			t.r.drop(ctx, err)
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

func (t *term) forward(ctx context.Context, c store.Change) error {
	if !work.KnownKind(c.Kind) {
		t.r.drop(ctx, fmt.Errorf("%w: unknown change kind %d", store.ErrCorruptChange, c.Kind))
		t.win.done(c.Position)
		return nil
	}
	if err := t.makeRoom(ctx); err != nil {
		return err
	}
	t.win.send(c.Position, work.Message(t.r.cfg.SubjectRoot, t.r.cfg.Partitions, work.RecordOf(c)))
	t.r.stats.forwarded.Add(1)
	t.win.collect()
	return nil
}
