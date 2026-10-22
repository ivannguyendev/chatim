package feed

import (
	"context"
	"time"
)

const (
	watchInterval   = 5 * time.Second
	jumpCallTimeout = 10 * time.Second
)

var jumpReasonLabels = [...]string{JumpLagTooOld: "lag_too_old", JumpConsumerLost: "consumer_lost"}

func (f *Feed) spawn(fn func()) bool {
	f.jobsMu.Lock()
	defer f.jobsMu.Unlock()
	if f.stopping {
		return false
	}
	f.jobs.Go(fn)
	return true
}

func (f *Feed) stopJobs() {
	f.jobsMu.Lock()
	f.stopping = true
	f.jobsMu.Unlock()
	f.jobs.Wait()
}

func (f *Feed) requestJump(r *receiver, reason JumpReason) {
	f.spawn(func() { f.jump(r, reason) })
}

func (f *Feed) jump(r *receiver, reason JumpReason) {
	ctx, cancel := context.WithTimeout(f.life, jumpCallTimeout)
	defer cancel()
	stream := r.stats.stream
	if old := r.detach(); old != nil && !closedWithin(ctx, old) {
		f.jumpFailed(r, reason, errConsumeStuck)
		return
	}
	if err := f.fan.Load().barrier(ctx); err != nil {
		f.jumpFailed(r, reason, err)
		return
	}
	b := *f.broker.Load()
	pos, err := b.Position(ctx, stream)
	if err != nil {
		f.jumpFailed(r, reason, err)
		return
	}
	if !r.publish {
		f.handler.Jumped(Jump{Reason: reason, Seq: pos.Last})
	}
	if err := f.consume(ctx, b, r, pos); err != nil {
		f.jumpFailed(r, reason, err)
		return
	}
	r.stats.jumps.Add(1)
	f.log.Warn("feed skipped to the stream end", "stream", stream, "reason", jumpReasonLabels[reason], "seq", pos.Last)
}

func (f *Feed) jumpFailed(r *receiver, reason JumpReason, err error) {
	if f.life.Err() != nil {
		return
	}
	r.stats.jumpFailures.Add(1)
	r.lose(f.life)
	f.log.Error("feed could not skip to the stream end; the watchdog tries again", "stream", r.stats.stream, "reason", jumpReasonLabels[reason], "err", err)
}

func (f *Feed) watch(ctx context.Context) {
	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for _, r := range f.readers {
			f.check(ctx, r)
		}
	}
}

func (f *Feed) check(ctx context.Context, r *receiver) {
	call, cancel := context.WithTimeout(ctx, jumpCallTimeout)
	pos, err := (*f.broker.Load()).Position(call, r.stats.stream)
	cancel()
	if !r.take(ctx) {
		return
	}
	reason, jump := r.verdict(pos, err, time.Now())
	r.give()
	if jump {
		f.requestJump(r, reason)
	}
}
