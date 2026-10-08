package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Reactions = (*Reactions)(nil)

type reactionKey struct {
	key  store.MsgKey
	user string
}

type Reactions struct {
	mu   sync.RWMutex
	docs map[reactionKey]domain.Reaction
	log  *Messages
}

func NewReactions() *Reactions { return &Reactions{docs: make(map[reactionKey]domain.Reaction)} }

func (s *Reactions) Set(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error) {
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
	k := reactionKey{key: store.ReactionKeyOf(r), user: r.User}
	cur, ok := s.docs[k]
	if ok && cur.Emoji == r.Emoji {
		return cur, false, nil
	}
	next := r
	next.Prev, next.N = cur.Emoji, cur.N+1
	s.docs[k] = next
	s.logChange(next)
	return next, true, nil
}

func (s *Reactions) Remove(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := reactionKey{key: key, user: user}
	cur, ok := s.docs[k]
	if !ok || cur.Emoji == "" {
		return cur, false, nil
	}
	next := cur
	next.Prev, next.Emoji, next.N, next.At = cur.Emoji, "", cur.N+1, at
	s.docs[k] = next
	s.logChange(next)
	return next, true, nil
}

func (s *Reactions) Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur, ok := s.docs[reactionKey{key: key, user: user}]
	return cur, ok, nil
}

func (s *Reactions) logChange(r domain.Reaction) {
	if s.log != nil {
		s.log.appendFact(logged{kind: store.ReactionChanged, reaction: r})
	}
}
