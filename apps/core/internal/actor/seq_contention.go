package actor

import (
	"context"

	"github.com/ivannguyendev/chatim/pkg/backoff"
)

func (a *actor) contend(e *entry) {
	a.stale = true
	a.contended = true
	if e.reassigns >= maxRequeues {
		a.r.yield(a)
	}
	a.requeue(e, false, errSeqContention)
}

func (a *actor) afterContention(ctx context.Context) {
	if !a.contended {
		a.contentionWait = 0
		return
	}
	a.contended = false
	if len(a.retries) == 0 || a.retireRequested() {
		return
	}
	a.contentionWait = min(max(2*a.contentionWait, contentionBackoff), maxContentionBackoff)
	backoff.Pause(ctx, backoff.Jitter(a.contentionWait))
}

func (r *Router) yield(a *actor) {
	r.yields.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	a.requestRetire()
}
