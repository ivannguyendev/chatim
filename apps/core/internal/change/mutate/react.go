package mutate

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type ReactCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	Emoji             string
}

type ReactResult struct {
	Change    uint32
	Reactions domain.ReactionSummary
}

func (m *Mutator) React(ctx context.Context, c ReactCmd) (ReactResult, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return ReactResult{}, err
	}
	if c.Emoji != "" {
		if err := domain.ValidateEmoji(c.Emoji); err != nil {
			return ReactResult{}, err
		}
		if !slices.Contains(m.d.Limits.Emojis, c.Emoji) {
			return ReactResult{}, domain.ErrEmojiNotAllowed
		}
	}
	grant, msg, err := m.target(ctx, access.ReactMessage, c.Tenant, c.User, key)
	if err != nil {
		return ReactResult{}, err
	}
	if c.Emoji != "" && msg.Deleted {
		return ReactResult{}, domain.ErrMessageDeleted
	}
	timer, err := m.armReactionCount(ctx, key)
	if err != nil {
		return ReactResult{}, err
	}
	doc, changed, err := m.writeReaction(ctx, c, key)
	if err != nil {
		return ReactResult{}, err
	}
	settle, done := settling(ctx)
	defer done()
	if !changed {
		m.d.CountTimers.Disarm(settle, timer)
		return ReactResult{Change: doc.N, Reactions: msg.Reactions}, nil
	}
	events := []*chatimv1.Event{pbconv.ReactionChanged(grant.Room.Type, doc)}
	summary, counted := m.countReaction(settle, msg.Reactions, key, timer, reactionDeltas(doc))
	if counted {
		msg.Reactions = summary
		events = append(events, pbconv.CountsChanged(grant.Room.Type, msg, m.now()))
	}
	_ = m.d.Events.Enqueue(key.Room, events)
	return ReactResult{Change: doc.N, Reactions: summary}, nil
}

func (m *Mutator) ReactionEmojis() []string { return slices.Clone(m.d.Limits.Emojis) }

func (m *Mutator) writeReaction(ctx context.Context, c ReactCmd, key store.MsgKey) (domain.Reaction, bool, error) {
	if c.Emoji == "" {
		return m.d.Interactions.RemoveReaction(ctx, key, c.User, m.now())
	}
	return m.d.Interactions.SetReaction(ctx, domain.Reaction{
		Room: key.Room, Thread: key.Thread, Seq: key.Seq, Tenant: c.Tenant, User: c.User, Emoji: c.Emoji, At: m.now(),
	})
}
