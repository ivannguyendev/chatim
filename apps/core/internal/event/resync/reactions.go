package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var ErrReactionPageFull = errors.New("resync: one instant holds more reactions than a reaction page")

type Reactions interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error)
}

func (s *scanner) reactions(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Reaction]{
		name:    "reactions",
		limit:   store.MaxReactionScan,
		full:    ErrReactionPageFull,
		between: s.deps.Reactions.Between,
		record:  reactionRecord,
		counted: &s.rep.ReactionRecords,
	})
}

func reactionRecord(r domain.Reaction) work.Record {
	return work.Record{Kind: store.ReactionChanged, Room: r.Room, Thread: r.Thread, Seq: r.Seq, Version: r.N, User: r.User, CommittedAt: r.At}
}
