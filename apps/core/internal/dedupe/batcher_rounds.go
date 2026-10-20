package dedupe

import (
	"context"
	"fmt"
	"sync"
)

type sized interface{ size() int }

func (b *Batcher) Run(context.Context) error {
	if !b.started.CompareAndSwap(false, true) {
		return errBatcherStarted
	}
	defer close(b.done)
	var wg sync.WaitGroup
	for _, s := range b.shards {
		wg.Go(func() { b.reserveLoop(s.reserves) })
		wg.Go(func() { b.settleLoop(s.settles) })
	}
	wg.Wait()
	return nil
}

func (b *Batcher) reserveLoop(in <-chan reserveCall) {
	next, held := reserveCall{}, false
	for {
		if !held {
			if next, held = receive(in, b.abort); !held {
				break
			}
		}
		if signalled(b.stopping) {
			break
		}
		var round []reserveCall
		round, next, held = collect(in, next, b.cfg.MaxKeys)
		b.reserveRound(round)
	}
	if held {
		next.out <- reserveResult{err: ErrBatcherClosed}
	}
	rejectReserves(in)
}

func rejectReserves(in <-chan reserveCall) {
	for c := range in {
		c.out <- reserveResult{err: ErrBatcherClosed}
	}
}

func (b *Batcher) discardQueued() {
	for _, s := range b.shards {
		rejectReserves(s.reserves)
		for c := range s.settles {
			b.dropped.Add(uint64(max(c.size(), 0)))
		}
	}
}

func (b *Batcher) reserveRound(round []reserveCall) {
	keys := round[0].keys
	if len(round) > 1 {
		keys = make([]Key, 0, total(round))
		for _, c := range round {
			keys = append(keys, c.keys...)
		}
	}
	verdicts, err := b.store.Reserve(context.Background(), keys)
	if err == nil && len(verdicts) != len(keys) {
		err = fmt.Errorf("cid batch reserve answered %d of %d keys", len(verdicts), len(keys))
	}
	off := 0
	for _, c := range round {
		res := reserveResult{err: err}
		if err == nil {
			res.verdicts = verdicts[off : off+len(c.keys) : off+len(c.keys)]
		}
		off += len(c.keys)
		c.out <- res
	}
}

func (b *Batcher) settleLoop(in <-chan settleCall) {
	next, held := settleCall{}, false
	for !signalled(b.abort) {
		if !held {
			if next, held = receive(in, b.abort); !held {
				return
			}
		}
		var batch []settleCall
		batch, next, held = collect(in, next, b.cfg.MaxKeys)
		b.settleRound(batch)
	}
}

func (b *Batcher) settleRound(batch []settleCall) {
	var commits []Entry
	var aborts []Key
	for _, c := range batch {
		commits = append(commits, c.commits...)
		aborts = append(aborts, c.aborts...)
	}
	if len(commits) > 0 {
		_ = b.store.Commit(context.Background(), commits)
	}
	if len(aborts) > 0 {
		_ = b.store.Abort(context.Background(), aborts)
	}
}

func receive[T any](in <-chan T, abort <-chan struct{}) (T, bool) {
	select {
	case c, ok := <-in:
		return c, ok
	case <-abort:
		var zero T
		return zero, false
	}
}

func collect[T sized](in <-chan T, first T, limit int) (batch []T, next T, held bool) {
	batch, n := []T{first}, first.size()
	for n < limit {
		select {
		case c, ok := <-in:
			if !ok {
				return batch, next, false
			}
			if n+c.size() > limit {
				return batch, c, true
			}
			batch, n = append(batch, c), n+c.size()
		default:
			return batch, next, false
		}
	}
	return batch, next, false
}

func total[T sized](batch []T) int {
	n := 0
	for _, c := range batch {
		n += c.size()
	}
	return n
}

func signalled(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
