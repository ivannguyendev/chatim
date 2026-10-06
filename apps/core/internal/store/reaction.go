package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxReactionScan = 1000

var (
	ErrStaleRead         = fmt.Errorf("read is behind a witnessed write: %w", apperr.ErrUnavailable)
	ErrReactionContended = fmt.Errorf("reaction write contended: %w", apperr.ErrUnavailable)
)

type Witness struct {
	User string
	N    uint32
}

type Reactions interface {
	Set(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error)
	Remove(ctx context.Context, key MsgKey, user string, at time.Time) (domain.Reaction, bool, error)
	Get(ctx context.Context, key MsgKey, user string) (domain.Reaction, bool, error)
	Count(ctx context.Context, key MsgKey, witnesses []Witness) ([]domain.ReactionCount, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error)
}

type ReactionSummaries interface {
	SetReactions(ctx context.Context, key MsgKey, base uint64, s domain.ReactionSummary) (bool, error)
}

func ReactionKeyOf(r domain.Reaction) MsgKey {
	return MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
}

func ValidateReaction(r domain.Reaction) error {
	if err := ValidateReactionTarget(ReactionKeyOf(r), r.User); err != nil {
		return err
	}
	if r.Tenant == "" {
		return invalid("tenant")
	}
	return nil
}

func ValidateReactionTarget(key MsgKey, user string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidUser(user)
}
