package interaction

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

func (s *Service) React(ctx context.Context, c ReactCmd) (ReactResult, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return ReactResult{}, err
	}
	if c.Emoji != "" {
		if err := domain.ValidateEmoji(c.Emoji); err != nil {
			return ReactResult{}, err
		}
		if !slices.Contains(s.d.Limits.Emojis, c.Emoji) {
			return ReactResult{}, domain.ErrEmojiNotAllowed
		}
	}
	grant, msg, err := s.d.Access.Target(ctx, s.d.Messages, access.ReactMessage, c.Tenant, c.User, key)
	if err != nil {
		return ReactResult{}, err
	}
	if c.Emoji != "" && msg.Deleted {
		return ReactResult{}, domain.ErrMessageDeleted
	}
	timer, err := s.armReactionCount(ctx, key)
	if err != nil {
		return ReactResult{}, err
	}
	doc, changed, err := s.writeReaction(ctx, c, key)
	if err != nil {
		return ReactResult{}, err
	}
	settle, done := settling(ctx)
	defer done()
	if !changed {
		s.d.CountTimers.Disarm(settle, timer)
		return ReactResult{Change: doc.N, Reactions: msg.Reactions}, nil
	}
	_ = s.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.ReactionChanged(grant.Room.Type, doc)})
	summary, counted := s.countReaction(settle, msg.Reactions, key, timer, reactionDeltas(doc))
	if counted {
		msg.Reactions = summary
		_ = s.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.CountsChanged(grant.Room.Type, msg, s.now())})
	}
	return ReactResult{Change: doc.N, Reactions: summary}, nil
}

func (s *Service) ReactionEmojis() []string { return slices.Clone(s.d.Limits.Emojis) }

func (s *Service) writeReaction(ctx context.Context, c ReactCmd, key store.MsgKey) (domain.Reaction, bool, error) {
	if c.Emoji == "" {
		return s.d.Interactions.RemoveReaction(ctx, key, c.User, s.now())
	}
	return s.d.Interactions.SetReaction(ctx, domain.Reaction{
		Room: key.Room, Thread: key.Thread, Seq: key.Seq, Tenant: c.Tenant, User: c.User, Emoji: c.Emoji, At: s.now(),
	})
}
