package interest

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var jumpResync = map[feed.JumpReason]chatimv1.ResyncReason{
	feed.JumpLagTooOld:    chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD,
	feed.JumpConsumerLost: chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST,
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

func (t *Table) schedule(users []*user, reason chatimv1.ResyncReason) {
	t.resyncMu.Lock()
	defer t.resyncMu.Unlock()
	if t.resyncStop != nil {
		t.resyncStop()
	}
	ctx, cancel := context.WithCancel(t.life)
	t.resyncStop = cancel
	if !t.spawn(func() { t.reloadAll(ctx, users, reason) }) {
		cancel()
	}
}

func (t *Table) reloadAll(ctx context.Context, users []*user, reason chatimv1.ResyncReason) {
	start := time.Now()
	for i, u := range users {
		if !sleepUntil(ctx, start.Add(t.cfg.ResyncWindow*time.Duration(i)/time.Duration(len(users)))) {
			return
		}
		if !t.acquire(ctx) {
			return
		}
		gen, ok := u.nextLoad()
		if !ok {
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
