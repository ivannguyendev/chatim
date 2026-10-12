package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ReactionCounts interface {
	AddReactionCounts(ctx context.Context, key store.MsgKey, deltas []store.EmojiDelta) (domain.ReactionSummary, error)
}

type MessageCountTimers interface {
	ArmMessageCountCheck(ctx context.Context, key store.MsgKey, counter string) (work.Timer, error)
	Disarm(ctx context.Context, t work.Timer)
}

func (m *Mutator) armReactionCount(ctx context.Context, key store.MsgKey) (work.Timer, error) {
	t, err := m.d.CountTimers.ArmMessageCountCheck(ctx, key, pbconv.ReactionsCounter)
	if err != nil {
		m.d.Log.WarnContext(ctx, "reaction count check timer not armed; reaction refused", "room", key.Room, "seq", key.Seq, "err", err)
		return work.Timer{}, domain.ErrRetryLater
	}
	return t, nil
}

func (m *Mutator) countReaction(ctx context.Context, cur domain.ReactionSummary, key store.MsgKey, t work.Timer, deltas []store.EmojiDelta) (domain.ReactionSummary, bool) {
	if len(deltas) == 0 {
		m.d.CountTimers.Disarm(ctx, t)
		return cur, false
	}
	next, err := m.d.Counts.AddReactionCounts(ctx, key, deltas)
	if err != nil {
		m.d.Log.WarnContext(ctx, "reaction counts not updated; the check timer will recount", "room", key.Room, "seq", key.Seq, "err", err)
		return domain.ReactionSummary{Counts: store.AddEmojiDeltas(cur.Counts, deltas), Version: cur.Version}, false
	}
	m.d.CountTimers.Disarm(ctx, t)
	return next, true
}

func reactionDeltas(r domain.Reaction) []store.EmojiDelta {
	if r.Prev == r.Emoji {
		return nil
	}
	var out []store.EmojiDelta
	if r.Prev != "" {
		out = append(out, store.EmojiDelta{Emoji: r.Prev, Delta: -1})
	}
	if r.Emoji != "" {
		out = append(out, store.EmojiDelta{Emoji: r.Emoji, Delta: 1})
	}
	return out
}
