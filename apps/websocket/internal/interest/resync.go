package interest

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var jumpResync = map[feed.JumpReason]chatimv1.ResyncReason{
	feed.JumpLagTooOld:    chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD,
	feed.JumpConsumerLost: chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST,
}

type reloadRun struct {
	queue    []*user
	queued   map[*user]struct{}
	reason   chatimv1.ResyncReason
	interval time.Duration
}

func (t *Table) Jumped(j feed.Jump) {
	reason, ok := jumpResync[j.Reason]
	if !ok {
		reason = chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST
	}
	t.lastSeq.Store(j.Seq)
	users := t.snapshot()
	for _, u := range users {
		u.mu.Lock()
		u.mark = min(u.mark, j.Seq)
		u.loadMark = min(u.loadMark, j.Seq)
		u.mu.Unlock()
	}
	t.schedule(users, reason)
}

func (t *Table) snapshot() []*user {
	var out []*user
	for i := range t.users {
		s := &t.users[i]
		s.mu.Lock()
		for _, u := range s.users {
			out = append(out, u)
		}
		s.mu.Unlock()
	}
	return out
}

func (u *user) reloadRank() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.ready {
		return 0
	}
	for c := range u.conns {
		if len(c.open) > 0 {
			return 1
		}
	}
	return 2
}

func (t *Table) schedule(users []*user, reason chatimv1.ResyncReason) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	run := t.reloads
	fresh := run == nil
	if fresh {
		run = &reloadRun{queued: map[*user]struct{}{}}
	}
	for _, u := range users {
		if _, ok := run.queued[u]; !ok {
			run.queued[u] = struct{}{}
			run.queue = append(run.queue, u)
		}
	}
	ranks := make(map[*user]int, len(run.queue))
	for _, u := range run.queue {
		ranks[u] = u.reloadRank()
	}
	slices.SortStableFunc(run.queue, func(a, b *user) int { return cmp.Compare(ranks[a], ranks[b]) })
	run.reason = reason
	if len(run.queue) > 0 {
		run.interval = t.cfg.ResyncWindow / time.Duration(len(run.queue))
	}
	if !fresh || len(run.queue) == 0 {
		return
	}
	t.reloads = run
	if !t.spawn(func() { t.reloadAll(run) }) {
		t.reloads = nil
	}
}

func (t *Table) nextReload(run *reloadRun) (*user, chatimv1.ResyncReason, time.Duration, bool) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	if len(run.queue) == 0 {
		t.reloads = nil
		return nil, 0, 0, false
	}
	u := run.queue[0]
	run.queue = run.queue[1:]
	delete(run.queued, u)
	return u, run.reason, run.interval, true
}

func (t *Table) reloadAll(run *reloadRun) {
	due := time.Now()
	for {
		if !sleepUntil(t.life, due) || !t.acquire(t.life) {
			return
		}
		u, reason, interval, ok := t.nextReload(run)
		if !ok {
			t.release()
			return
		}
		due = due.Add(interval)
		gen, live := u.nextLoad()
		if !live {
			t.release()
			continue
		}
		if !t.spawn(func() {
			defer t.release()
			t.fetch(u, gen, reason)
		}) {
			t.release()
			return
		}
	}
}

func (u *user) nextLoad() (uint64, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone {
		return 0, false
	}
	u.gen++
	return u.gen, true
}

func sleepUntil(ctx context.Context, at time.Time) bool {
	wait := time.Until(at)
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
