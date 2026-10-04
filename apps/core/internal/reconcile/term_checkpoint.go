package reconcile

import (
	"bytes"
	"context"
	"time"
)

func (t *term) checkpoint(ctx context.Context) error {
	t.checked = time.Now()
	t.win.collect()
	t.confirm(ctx)
	if !t.r.leading() {
		return errLostLead
	}
	return nil
}

func (t *term) checkpointIfDue(ctx context.Context) error {
	if time.Since(t.checked) < t.r.cfg.ConfirmEvery {
		return nil
	}
	return t.checkpoint(ctx)
}

func (t *term) pause(ctx context.Context, until time.Time) error {
	for {
		left := time.Until(until)
		if left <= 0 {
			return nil
		}
		if err := sleep(ctx, t.r.stop, min(left, t.r.cfg.ConfirmEvery)); err != nil {
			return err
		}
		if err := t.checkpointIfDue(ctx); err != nil {
			return err
		}
	}
}

func (t *term) makeRoom(ctx context.Context) error {
	for {
		t.win.collect()
		if !t.win.full() {
			return nil
		}
		if err := t.win.awaitHead(ctx, t.r.stop, t.r.cfg.ConfirmEvery); err != nil {
			return err
		}
		if err := t.checkpointIfDue(ctx); err != nil {
			return err
		}
	}
}

func (t *term) settle(ctx context.Context) {
	t.win.drain(ctx)
	t.confirm(ctx)
}

func (t *term) confirm(ctx context.Context) {
	pos := t.win.settled
	if pos == nil || bytes.Equal(pos, t.confirmed) {
		return
	}
	if err := t.cur.Confirm(ctx, pos); err != nil {
		t.r.log.WarnContext(ctx, "confirm change feed position", "err", err)
		return
	}
	t.confirmed = pos
}
