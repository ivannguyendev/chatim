package actor

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (a *actor) markActive(ctx context.Context) {
	now := time.Now()
	if !a.markedAt.IsZero() && now.Sub(a.markedAt) < activeMarkEvery {
		return
	}
	if a.r.marks.MarkActive(ctx, a.id) == nil {
		a.markedAt = now
	}
}

func (a *actor) publishLanded() {
	if len(a.landed) == 0 {
		return
	}
	events := make([]*chatimv1.Event, len(a.landed))
	for i, l := range a.landed {
		events[i] = pbconv.MessageCreated(a.room.Type, l.msg)
	}
	slices.SortFunc(events, func(x, y *chatimv1.Event) int { return cmp.Compare(x.GetPts(), y.GetPts()) })
	_ = a.r.events.Enqueue(a.id, events)
}
