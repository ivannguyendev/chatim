package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (e *ReplyMentionIndex) mention(ctx context.Context, m domain.Message) error {
	return e.deps.Mentions.ApplyMentions(ctx, store.MentionSet{
		Key:       store.KeyOf(m),
		Tenant:    m.Tenant,
		Sender:    m.From,
		Ver:       m.Version,
		Targets:   domain.MentionTargetsOf(m),
		CreatedAt: m.CreatedAt,
		At:        e.now(),
	})
}
