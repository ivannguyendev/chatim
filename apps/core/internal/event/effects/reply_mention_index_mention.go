package effects

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (e *ReplyMentionIndex) mention(ctx context.Context, m domain.Message) error {
	targets := domain.MentionTargetsOf(m)
	err := e.deps.Mentions.ApplyMentions(ctx, store.MentionSet{
		Key:       store.KeyOf(m),
		Tenant:    m.Tenant,
		Sender:    m.From,
		Ver:       m.Version,
		Targets:   targets,
		CreatedAt: m.CreatedAt,
		At:        e.now(),
	})
	if err != nil || !slices.ContainsFunc(targets, shared) {
		return err
	}
	return e.deps.Shared.RaiseSharedMention(ctx, m.Room, m.CreatedAt)
}

func shared(t domain.MentionTarget) bool {
	return t.Kind == domain.MentionAll || t.Kind == domain.MentionGroup
}
