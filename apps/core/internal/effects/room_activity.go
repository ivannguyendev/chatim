package effects

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type ActivityWriter interface {
	TouchActivity(ctx context.Context, acts []store.Activity) error
}

type RoomActivity struct {
	rooms ActivityWriter
}

type activityKey struct{ room, thread uint64 }

const RoomActivityName = "room_activity"

func NewRoomActivity(rooms ActivityWriter) *RoomActivity { return &RoomActivity{rooms: rooms} }

func (a *RoomActivity) Effect() Effect {
	return Effect{Name: RoomActivityName, Run: a.run}
}

func (a *RoomActivity) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	if len(recs) == 0 {
		return errs
	}
	if err := a.rooms.TouchActivity(ctx, latestActivity(recs)); err != nil {
		err = fmt.Errorf("room activity: %w", err)
		for i := range errs {
			errs[i] = err
		}
	}
	return errs
}

func latestActivity(recs []work.Record) []store.Activity {
	at := make(map[activityKey]int, len(recs))
	out := make([]store.Activity, 0, len(recs))
	for _, r := range recs {
		k := activityKey{r.Room, r.Thread}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			out = append(out, store.Activity{Room: r.Room, Thread: r.Thread, Seq: r.Seq, At: r.CommittedAt})
			continue
		}
		out[i].Seq = max(out[i].Seq, r.Seq)
		if r.CommittedAt.After(out[i].At) {
			out[i].At = r.CommittedAt
		}
	}
	return out
}
