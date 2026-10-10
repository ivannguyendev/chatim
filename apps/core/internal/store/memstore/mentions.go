package memstore

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Mentions = (*Mentions)(nil)

type Mentions struct {
	mu   sync.Mutex
	docs map[store.MsgKey]map[domain.MentionTarget]domain.Mention
}

func NewMentions() *Mentions {
	return &Mentions{docs: make(map[store.MsgKey]map[domain.MentionTarget]domain.Mention)}
}

func (s *Mentions) ApplyMentions(ctx context.Context, set store.MentionSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMentionSet(set); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.docs[set.Key]
	plan := store.PlanMentions(slices.Collect(maps.Values(cur)), set)
	if len(plan.Live)+len(plan.Retire) == 0 {
		return nil
	}
	if cur == nil {
		cur = make(map[domain.MentionTarget]domain.Mention)
		s.docs[set.Key] = cur
	}
	for _, t := range plan.Live {
		cur[t] = mentionOf(set, t, true)
	}
	for _, t := range plan.Retire {
		cur[t] = mentionOf(set, t, false)
	}
	return nil
}

func (s *Mentions) MentionsOf(ctx context.Context, key store.MsgKey) ([]domain.Mention, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.docs[key])), nil
}

func mentionOf(set store.MentionSet, t domain.MentionTarget, live bool) domain.Mention {
	return domain.Mention{
		Key: domain.MsgKey(set.Key), Tenant: set.Tenant, Target: t, Sender: set.Sender,
		Live: live, Ver: set.Ver, CreatedAt: set.CreatedAt, UpdatedAt: set.At,
	}
}
