package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var ErrReactionPageFull = errors.New("resync: one instant holds more reactions than a reaction page")

type Interactions interface {
	Between(ctx context.Context, room uint64, kind keys.InteractionKind, from, to time.Time, limit int) ([]store.Interaction, error)
}

func (s *scanner) reactions(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[store.Interaction]{
		name:  "reactions",
		limit: store.MaxInteractionScan,
		full:  ErrReactionPageFull,
		between: func(ctx context.Context, room uint64, from, to time.Time, limit int) ([]store.Interaction, error) {
			return s.deps.Interactions.Between(ctx, room, keys.ReactionKind, from, to, limit)
		},
		record:  reactionRecord,
		counted: &s.rep.ReactionRecords,
	})
}

func reactionRecord(r store.Interaction) work.Record {
	return work.Record{Kind: store.ReactionChanged, Room: r.Key.Room, Thread: r.Key.Thread, Seq: r.Key.Seq, Version: r.Ver, User: r.User, CommittedAt: r.At}
}
