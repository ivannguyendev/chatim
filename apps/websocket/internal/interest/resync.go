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

const retryDelay = time.Second

type retry struct {
	user *user
	at   time.Time
}

type reloadRun struct {
	front   []*user
	queue   []*user
	queued  map[*user]struct{}
	later   []retry
	delayed map[*user]struct{}
	reason  chatimv1.ResyncReason
}

func (t *Table) Jumped(j feed.Jump) {
	reason, ok := jumpResync[j.Reason]
	if !ok {
		reason = chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST
	}
	t.jumpMu.Lock()
	defer t.jumpMu.Unlock()
	if t.isClosed() {
		return
	}
	t.lastSeq.Store(j.Seq)
	now := time.Now()
	users := t.snapshot()
	for _, u := range users {
		u.mu.Lock()
		t.suspend(u, now)
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
	open := false
	for c := range u.conns {
		if !c.readied {
			return 0
		}
		open = open || len(c.open) > 0
	}
	if open {
		return 1
	}
	return 2
}

func (t *Table) schedule(users []*user, reason chatimv1.ResyncReason) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	run, fresh := t.currentRun(reason)
	run.reason = reason
	queue := make([]*user, 0, len(run.queued)+len(users))
	seen := make(map[*user]struct{}, cap(queue))
	for _, u := range slices.Concat(run.front, run.queue) {
		if _, ok := run.queued[u]; ok {
			if _, dup := seen[u]; !dup {
				seen[u] = struct{}{}
				queue = append(queue, u)
			}
		}
	}
	for _, u := range users {
		_, waiting := run.delayed[u]
		if _, ok := seen[u]; !ok && !waiting {
			seen[u] = struct{}{}
			run.queued[u] = struct{}{}
			queue = append(queue, u)
		}
	}
	ranks := make(map[*user]int, len(queue))
	for _, u := range queue {
		ranks[u] = u.reloadRank()
	}
	slices.SortStableFunc(queue, func(a, b *user) int { return cmp.Compare(ranks[a], ranks[b]) })
	run.front, run.queue = nil, queue
	if fresh {
		t.startRun(run)
	}
}

func (t *Table) requeue(u *user, reason chatimv1.ResyncReason) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	run, fresh := t.currentRun(reason)
	_, queued := run.queued[u]
	if _, waiting := run.delayed[u]; !queued && !waiting {
		run.delayed[u] = struct{}{}
		run.later = append(run.later, retry{user: u, at: time.Now().Add(retryDelay)})
	}
	if fresh {
		t.startRun(run)
	}
}

func (run *reloadRun) promote(now time.Time) {
	for len(run.later) > 0 && !run.later[0].at.After(now) {
		u := run.later[0].user
		run.later = run.later[1:]
		delete(run.delayed, u)
		if _, ok := run.queued[u]; ok {
			continue
		}
		run.queued[u] = struct{}{}
		if u.reloadRank() == 0 {
			run.front = append(run.front, u)
			continue
		}
		run.queue = append(run.queue, u)
	}
}

func (t *Table) prioritize(u *user) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	if run := t.reloads; run != nil {
		if _, ok := run.queued[u]; ok {
			run.front = append(run.front, u)
		}
	}
}

func (t *Table) currentRun(reason chatimv1.ResyncReason) (*reloadRun, bool) {
	if t.reloads != nil {
		return t.reloads, false
	}
	return &reloadRun{queued: map[*user]struct{}{}, delayed: map[*user]struct{}{}, reason: reason}, true
}

func (t *Table) startRun(run *reloadRun) {
	if len(run.queue) == 0 && len(run.later) == 0 {
		return
	}
	t.reloads = run
	if !t.spawn(func() { t.reloadAll(run) }) {
		t.reloads = nil
	}
}

func (t *Table) nextReload(run *reloadRun, now time.Time) (*user, chatimv1.ResyncReason, time.Time, bool) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	run.promote(now)
	for {
		var u *user
		switch {
		case len(run.front) > 0:
			u, run.front = run.front[0], run.front[1:]
		case len(run.queue) > 0:
			u, run.queue = run.queue[0], run.queue[1:]
		case len(run.later) > 0:
			return nil, run.reason, run.later[0].at, true
		default:
			t.reloads = nil
			return nil, 0, time.Time{}, false
		}
		if _, ok := run.queued[u]; ok {
			delete(run.queued, u)
			return u, run.reason, time.Time{}, true
		}
	}
}

func (t *Table) reloadAll(run *reloadRun) {
	interval := time.Second / time.Duration(t.cfg.ReloadRate)
	due := time.Now()
	for {
		if !sleepUntil(t.life, due) {
			return
		}
		u, reason, wake, ok := t.nextReload(run, time.Now())
		switch {
		case !ok:
			return
		case u == nil:
			due = wake
			continue
		}
		gen, live := u.reloadGen()
		if !live {
			continue
		}
		if !t.acquire(t.life) {
			return
		}
		if now := time.Now(); due.Before(now) {
			due = now
		}
		due = due.Add(interval)
		if !t.spawn(func() {
			defer t.release()
			t.fetch(u, gen, reason)
		}) {
			t.release()
			return
		}
	}
}

func (u *user) reloadGen() (uint64, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.gone || !u.suspended {
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
