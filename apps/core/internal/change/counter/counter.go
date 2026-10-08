package counter

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	store.ReactionSummaries
}

type Reactions interface {
	Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error)
}

var (
	ErrContended = fmt.Errorf("reaction summary contended: %w", apperr.ErrUnavailable)

	errMissingDeps = fmt.Errorf("%w: counter needs messages and reactions", apperr.ErrInvalidArgument)
	errNoTries     = fmt.Errorf("%w: touch needs at least one try", apperr.ErrInvalidArgument)
)

type Toucher struct {
	msgs      Messages
	reactions Reactions
}

func New(msgs Messages, reactions Reactions) (*Toucher, error) {
	if msgs == nil || reactions == nil {
		return nil, errMissingDeps
	}
	return &Toucher{msgs: msgs, reactions: reactions}, nil
}

func (t *Toucher) Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	if tries < 1 {
		return cur, false, errNoTries
	}
	for range tries {
		counts, err := t.reactions.Count(ctx, key, witnesses)
		if err != nil {
			return cur, false, err
		}
		if slices.Equal(counts, cur.Counts) {
			return cur, false, nil
		}
		next := domain.ReactionSummary{Counts: counts, Version: cur.Version + 1}
		ok, err := t.msgs.SetReactions(ctx, key, cur.Version, next)
		if err != nil {
			return cur, false, err
		}
		if ok {
			return next, true, nil
		}
		if cur, err = t.reload(ctx, key); err != nil {
			return cur, false, err
		}
	}
	return cur, false, fmt.Errorf("reactions of %d/%d/%d after %d tries: %w", key.Room, key.Thread, key.Seq, tries, ErrContended)
}

func (t *Toucher) reload(ctx context.Context, key store.MsgKey) (domain.ReactionSummary, error) {
	msgs, err := t.msgs.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	if len(msgs) == 0 {
		return domain.ReactionSummary{}, fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, domain.ErrMessageNotFound)
	}
	return msgs[0].Reactions, nil
}
