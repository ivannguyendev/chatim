package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReactionSummaries = (*Messages)(nil)

func (s *Messages) SetReactions(ctx context.Context, key store.MsgKey, base uint64, sum domain.ReactionSummary) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, sum.Version); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.lines[timeline{key.Room, key.Thread}]
	i, found := slices.BinarySearchFunc(line, key.Seq, bySeq)
	if !found || line[i].Reactions.Version != base {
		return false, nil
	}
	line[i].Reactions = domain.ReactionSummary{Counts: slices.Clone(sum.Counts), Version: sum.Version}
	return true, nil
}
