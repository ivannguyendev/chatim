package actor

import (
	"context"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func (r *Router) EvictSlots(ctx context.Context, slots []uint16) {
	for _, gone := range r.retireSlots(slots) {
		select {
		case <-gone:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Router) retireSlots(slots []uint16) []<-chan struct{} {
	if len(slots) == 0 {
		return nil
	}
	var moved [slotmap.Count]bool
	for _, s := range slots {
		if int(s) < slotmap.Count {
			moved[s] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.closed {
		return nil
	}
	var gone []<-chan struct{}
	for room, a := range r.actors {
		if !moved[slotmap.Of(room)] {
			continue
		}
		a.requestRetire()
		gone = append(gone, a.gone)
	}
	return gone
}

func (a *actor) retireRequested() bool {
	select {
	case <-a.retire:
		return true
	default:
		return false
	}
}

func (a *actor) requestRetire() {
	if !a.retireRequested() {
		close(a.retire)
	}
}

func (a *actor) retireNow(ctx context.Context, first *request) {
	if first != nil {
		first.answer(Ack{}, errRetired)
	}
	for _, e := range a.retries {
		a.fail(e, errRetired, e.fixed)
	}
	a.retries = nil
	a.conclude(ctx)
	a.exit(errRetired)
}
