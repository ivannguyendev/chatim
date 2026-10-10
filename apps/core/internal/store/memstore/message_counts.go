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
	raw, held := s.rawCounts[key]
	if !held {
		raw = domain.RawCounts(m.Reactions.Counts)
	}
	raw = store.AddRawEmojiDeltas(raw, deltas)
	m.Reactions = domain.SettleReactions(raw, m.Reactions.Version+1)
	s.holdRaw(key, m.Reactions.Unsettled, raw)
	out := m.Reactions
	out.Counts = slices.Clone(out.Counts)
	return out, nil
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
	n, held := s.rawReply[key]
	if !held {
		n = int64(m.Replies.N)
	}
	n += int64(delta)
	m.Replies = domain.SettleReplies(n, m.Replies.Version+1)
	if m.Replies.Unsettled {
		s.rawReply[key] = n
	} else {
		delete(s.rawReply, key)
	}
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
	delete(s.rawReply, key)
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

func (s *Messages) holdRaw(key store.MsgKey, unsettled bool, raw []domain.RawCount) {
	if unsettled {
		s.rawCounts[key] = raw
		return
	}
	delete(s.rawCounts, key)
}
