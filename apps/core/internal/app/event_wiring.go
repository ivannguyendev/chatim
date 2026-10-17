package app

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireStateClears(cfg config.Config, st *mongostore.Store, log *slog.Logger) (*stateclear.Queue, error) {
	q, err := stateclear.New(st.EventStates(), cfg.StateClear, log)
	if err != nil {
		return nil, fmt.Errorf("wire state clear queue: %w", err)
	}
	return q, nil
}

func wireProjections(cfg config.Config, cl *clients, st *mongostore.Store, owner projection.Owner, events projection.EventQueue, timers projection.LinkTimers, log *slog.Logger) (*projection.Consumer, error) {
	replies, err := projection.NewReplyIndex(projection.ReplyIndexDeps{
		Replies: st.Interactions(), Threads: st.ReplyThreads(), Counts: st, Timers: timers, Events: events,
	})
	if err != nil {
		return nil, fmt.Errorf("wire %s projection: %w", projection.ReplyIndexName, err)
	}
	handlers := []projection.Projection{projection.NewRoomActivity(st), replies}
	c, err := projection.NewConsumer(handlers, projection.NewSource(cl.js, cfg.Projection), cfg.Projection, log, projection.WithLeader(owner, sweep.LeaderSlot, cfg.Slot.Tick))
	if err != nil {
		return nil, fmt.Errorf("wire projection consumer: %w", err)
	}
	return c, nil
}
