package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageCounts = (*Messages)(nil)

func (s *Messages) AddReactionCounts(ctx context.Context, key store.MsgKey, deltas []store.EmojiDelta) (domain.ReactionSummary, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReactionSummary{}, err
	}
	if err := key.Validate(); err != nil {
		return domain.ReactionSummary{}, err
	}
	if err := store.ValidateEmojiDeltas(deltas); err != nil {
		return domain.ReactionSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.messageLocked(key)
	if !ok {
		return domain.ReactionSummary{}, domain.ErrMessageNotFound
	}
	m.Reactions = domain.ReactionSummary{Counts: store.AddEmojiDeltas(m.Reactions.Counts, deltas), Version: m.Reactions.Version + 1}
	return domain.ReactionSummary{Counts: slices.Clone(m.Reactions.Counts), Version: m.Reactions.Version}, nil
}

func (s *Messages) AddReplyCount(ctx context.Context, key store.MsgKey, delta int) (domain.ReplyCount, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReplyCount{}, err
	}
	if err := key.Validate(); err != nil {
		return domain.ReplyCount{}, err
	}
	if err := store.ValidateCountDelta(delta); err != nil {
		return domain.ReplyCount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.messageLocked(key)
	if !ok {
		return domain.ReplyCount{}, domain.ErrMessageNotFound
	}
	m.Replies = domain.ReplyCount{N: clampCount(int64(m.Replies.N) + int64(delta)), Version: m.Replies.Version + 1}
	return m.Replies, nil
}

func (s *Messages) SetReplyCount(ctx context.Context, key store.MsgKey, base uint64, n uint32) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.messageLocked(key)
	if !ok || m.Replies.Version != base {
		return false, nil
	}
	m.Replies = domain.ReplyCount{N: n, Version: base + 1}
	return true, nil
}

func (s *Messages) messageLocked(key store.MsgKey) (*domain.Message, bool) {
	line := s.lines[timeline{key.Room, key.Thread}]
	i, found := slices.BinarySearchFunc(line, key.Seq, bySeq)
	if !found {
		return nil, false
	}
	return &line[i], true
}

func clampCount(n int64) uint32 {
	return uint32(min(max(n, 0), int64(^uint32(0))))
}
