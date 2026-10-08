package effects

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errShortResult = errors.New("effect returned fewer results than records")

type group struct {
	kind store.ChangeKind
	ds   []work.Delivery
	recs []work.Record
}

func (w *Workers) process(ctx context.Context, p int, ds []work.Delivery) {
	groups := groupByKind(ds)
	var lag time.Duration
	for i, g := range groups {
		done, late := w.runGroup(ctx, g)
		lag = max(lag, late)
		if !done {
			for _, rest := range groups[i:] {
				w.release(ctx, rest.ds)
			}
			break
		}
	}
	w.lags[p].Store(int64(lag))
}

func groupByKind(ds []work.Delivery) []group {
	var out []group
	at := map[store.ChangeKind]int{}
	for _, d := range ds {
		r := d.Record()
		i, ok := at[r.Kind]
		if !ok {
			i = len(out)
			at[r.Kind] = i
			out = append(out, group{kind: r.Kind})
		}
		out[i].ds = append(out[i].ds, d)
		out[i].recs = append(out[i].recs, r)
	}
	slices.SortFunc(out, func(a, b group) int { return cmp.Compare(a.kind, b.kind) })
	return out
}

func (w *Workers) runGroup(ctx context.Context, g group) (bool, time.Duration) {
	failed := make([]bool, len(g.recs))
	newest, oldest := committedRange(g.recs)
	var lag time.Duration
	for _, e := range w.deps.Registry[g.kind] {
		if !w.waitUntil(ctx, newest.Add(e.Delay)) {
			return false, lag
		}
		lag = max(lag, time.Since(oldest.Add(e.Delay)))
		errs := e.Run(ctx, g.recs)
		for i := range failed {
			if errAt(errs, i) != nil {
				failed[i] = true
			}
		}
	}
	w.settle(ctx, g.ds, failed)
	return true, lag
}

func (w *Workers) waitUntil(ctx context.Context, due time.Time) bool {
	left := time.Until(due)
	if left <= 0 {
		return true
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-w.stop:
		return false
	case <-ctx.Done():
		return false
	}
}

func (w *Workers) settle(ctx context.Context, ds []work.Delivery, failed []bool) {
	stopped := ctx.Err() != nil
	for i, d := range ds {
		var err error
		switch {
		case !failed[i]:
			if err = d.Ack(); err == nil {
				w.processed.Add(1)
			}
		case stopped:
			err = d.Nak(0)
		default:
			w.failed.Add(1)
			err = d.Nak(w.cfg.RetryDelay)
		}
		if err != nil {
			w.fails.warn(ctx, settleFailedMsg, "err", err)
		}
	}
}

func committedRange(recs []work.Record) (time.Time, time.Time) {
	newest, oldest := recs[0].CommittedAt, recs[0].CommittedAt
	for _, r := range recs[1:] {
		if r.CommittedAt.After(newest) {
			newest = r.CommittedAt
		}
		if r.CommittedAt.Before(oldest) {
			oldest = r.CommittedAt
		}
	}
	return newest, oldest
}

func errAt(errs []error, i int) error {
	if i < len(errs) {
		return errs[i]
	}
	return errShortResult
}
