package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Interactions = (*Interactions)(nil)

type userKey struct {
	key  store.MsgKey
	user string
}

type replyKey struct {
	parent, reply store.MsgKey
}

type Interactions struct {
	mu        sync.RWMutex
	reactions map[userKey]domain.Reaction
	bookmarks map[userKey]domain.Bookmark
	replies   map[replyKey]domain.Reply
	log       *Messages
}

func NewInteractions() *Interactions {
	return &Interactions{
		reactions: make(map[userKey]domain.Reaction),
		bookmarks: make(map[userKey]domain.Bookmark),
		replies:   make(map[replyKey]domain.Reply),
	}
}

func (s *Interactions) SetReaction(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := store.ValidateReaction(r); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := domain.ValidateEmoji(r.Emoji); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := userKey{key: store.ReactionKeyOf(r), user: r.User}
	cur, ok := s.reactions[k]
	if ok && cur.Emoji == r.Emoji {
		return cur, false, nil
	}
	next := r
	next.Prev, next.N = cur.Emoji, cur.N+1
	s.reactions[k] = next
	s.logReaction(next)
	return next, true, nil
}

func (s *Interactions) RemoveReaction(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := userKey{key: key, user: user}
	cur, ok := s.reactions[k]
	if !ok || cur.Emoji == "" {
		return cur, false, nil
	}
	next := cur
	next.Prev, next.Emoji, next.N, next.At = cur.Emoji, "", cur.N+1, at
	s.reactions[k] = next
	s.logReaction(next)
	return next, true, nil
}

func (s *Interactions) GetReaction(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur, ok := s.reactions[userKey{key: key, user: user}]
	return cur, ok, nil
}

func (s *Interactions) logReaction(r domain.Reaction) {
	if s.log != nil {
		s.log.appendFact(logged{kind: store.ReactionChanged, reaction: r})
	}
}
