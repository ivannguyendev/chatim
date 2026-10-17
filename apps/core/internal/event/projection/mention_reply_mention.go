package projection

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (x *MentionReplyIndex) mention(ctx context.Context, m domain.Message) error {
	targets := domain.MentionTargetsOf(m)
	err := x.deps.Mentions.ApplyMentions(ctx, store.MentionSet{
		Key:       store.KeyOf(m),
		Tenant:    m.Tenant,
		Sender:    m.From,
		Ver:       m.Version,
		Targets:   targets,
		CreatedAt: m.CreatedAt,
		At:        x.now(),
	})
	if err != nil || !slices.ContainsFunc(targets, sharedTarget) {
		return err
	}
	return x.deps.Shared.RaiseSharedMention(ctx, m.Room, m.CreatedAt)
}

func sharedTarget(t domain.MentionTarget) bool {
	return t.Kind == domain.MentionAll || t.Kind == domain.MentionGroup
}
