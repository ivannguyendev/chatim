package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

type projectionSet struct {
	defs      []projection.Projection
	consumers []*projection.Consumer
}

func wireStateClears(cfg config.Config, st *mongostore.Store, log *slog.Logger) (*stateclear.Queue, error) {
	q, err := stateclear.New(st.EventStates(), cfg.StateClear, log)
	if err != nil {
		return nil, fmt.Errorf("wire state clear queue: %w", err)
	}
	return q, nil
}

func wireProjections(cfg config.Config, cl *clients, st *mongostore.Store, events projection.EventQueue, timers projection.LinkTimers, log *slog.Logger) (projectionSet, error) {
	replies, err := projection.NewMentionReplyIndex(projection.MentionReplyIndexDeps{
		Replies: st.Interactions(), Threads: st.ReplyThreads(), Counts: st, Mentions: st.Mentions(), Shared: st, Timers: timers, Events: events,
	})
	if err != nil {
		return projectionSet{}, fmt.Errorf("wire %s projection: %w", projection.MentionReplyIndexName, err)
	}
	reads, err := projection.NewMentionRead(st, st.Mentions())
	if err != nil {
		return projectionSet{}, fmt.Errorf("wire %s projection: %w", projection.MentionReadName, err)
	}
	set := projectionSet{defs: []projection.Projection{projection.NewRoomActivity(st), replies, reads}}
	for _, p := range set.defs {
		c, err := projection.NewConsumer(p, projection.NewSource(cl.js, cfg.Projection, p), cfg.Projection, log)
		if err != nil {
			return projectionSet{}, fmt.Errorf("wire %s projection consumer: %w", p.Name(), err)
		}
		set.consumers = append(set.consumers, c)
	}
	return set, nil
}

func (s projectionSet) ensure(ctx context.Context, cfg config.Config, js projection.Admin) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	for _, p := range s.defs {
		if err := projection.EnsureConsumer(ctx, js, cfg.Projection, p); err != nil {
			return config.RedactError(err, cfg.NATSURL)
		}
	}
	return nil
}

func (s projectionSet) group() drainerGroup {
	g := make(drainerGroup, len(s.consumers))
	for i, c := range s.consumers {
		g[i] = c
	}
	return g
}
