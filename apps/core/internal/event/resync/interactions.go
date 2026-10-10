package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type Interactions interface {
	Between(ctx context.Context, q store.InteractionScan) ([]store.Interaction, error)
}

type countCheck struct {
	key     store.MsgKey
	counter string
}

func (s *scanner) interactions(ctx context.Context, room uint64) error {
	for _, kind := range []keys.InteractionKind{keys.ReactionKind, keys.BookmarkKind, keys.ReplyKind} {
		if err := s.interactionsOf(ctx, room, kind); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) interactionsOf(ctx context.Context, room uint64, kind keys.InteractionKind) error {
	q := store.InteractionScan{Room: room, Kind: kind, From: s.opts.From, To: s.opts.To, Limit: store.MaxInteractionScan}
	for {
		page, err := s.deps.Interactions.Between(ctx, q)
		if err != nil {
			return fmt.Errorf("interactions of kind %d in room %d: %w", kind, room, err)
		}
		for _, x := range page {
			if err := s.interaction(ctx, x); err != nil {
				return err
			}
		}
		if len(page) < q.Limit {
			return nil
		}
		q.After = &page[len(page)-1]
	}
}

func (s *scanner) interaction(ctx context.Context, x store.Interaction) error {
	switch x.Kind {
	case keys.ReactionKind:
		s.checkCount(x.Key, pbconv.ReactionsCounter)
		return s.emitCounted(ctx, interactionRecord(store.ReactionChanged, x), &s.rep.ReactionRecords)
	case keys.BookmarkKind:
		return s.emitCounted(ctx, interactionRecord(store.BookmarkChanged, x), &s.rep.BookmarkRecords)
	default:
		s.checkCount(x.Key, pbconv.RepliesCounter)
		return nil
	}
}

func interactionRecord(kind store.ChangeKind, x store.Interaction) work.Record {
	return work.Record{Kind: kind, Room: x.Key.Room, Thread: x.Key.Thread, Seq: x.Key.Seq, Version: x.Ver, User: x.User, CommittedAt: x.At}
}

func (s *scanner) checkCount(key store.MsgKey, counter string) {
	c := countCheck{key: key, counter: counter}
	if _, ok := s.seen[c]; ok {
		return
	}
	s.seen[c] = struct{}{}
	s.checks = append(s.checks, c)
}

func (s *scanner) countChecks(ctx context.Context, _ uint64) error {
	checks := s.checks
	s.checks, s.seen = nil, map[countCheck]struct{}{}
	for _, c := range checks {
		rec, err := work.MessageCountCheck(c.key, c.counter, work.RandomOp(), time.Now().UTC())
		if err != nil {
			return fmt.Errorf("count check of %d/%d/%d %s: %w", c.key.Room, c.key.Thread, c.key.Seq, c.counter, err)
		}
		if err := s.emitCounted(ctx, rec, &s.rep.CountCheckRecords); err != nil {
			return err
		}
	}
	return nil
}
