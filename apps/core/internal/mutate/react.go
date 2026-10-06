package mutate

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type CounterToucher interface {
	Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
}

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
	doc, changed, err := m.writeReaction(ctx, c, key)
	if err != nil {
		return ReactResult{}, err
	}
	if !changed {
		return ReactResult{Change: doc.N, Reactions: msg.Reactions}, nil
	}
	_ = m.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.ReactionChanged(grant.Room.Type, doc)})
	summary := m.touch(ctx, grant.Room.Type, msg, store.Witness{User: c.User, N: doc.N})
	return ReactResult{Change: doc.N, Reactions: summary}, nil
}

func (m *Mutator) ReactionEmojis() []string { return slices.Clone(m.d.Limits.Emojis) }

func (m *Mutator) writeReaction(ctx context.Context, c ReactCmd, key store.MsgKey) (domain.Reaction, bool, error) {
	if c.Emoji == "" {
		return m.d.Reactions.Remove(ctx, key, c.User, m.now())
	}
	return m.d.Reactions.Set(ctx, domain.Reaction{
		Room: key.Room, Thread: key.Thread, Seq: key.Seq, Tenant: c.Tenant, User: c.User, Emoji: c.Emoji, At: m.now(),
	})
}

func (m *Mutator) touch(ctx context.Context, typ domain.RoomType, msg domain.Message, w store.Witness) domain.ReactionSummary {
	key := store.KeyOf(msg)
	summary, bumped, err := m.d.Counter.Touch(ctx, key, msg.Reactions, []store.Witness{w}, FastTouchTries)
	if err != nil {
		return msg.Reactions
	}
	if bumped {
		msg.Reactions = summary
		_ = m.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.CountsChanged(typ, msg, m.now())})
	}
	return summary
}
