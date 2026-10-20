package message

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Forwarded struct {
	Text   string
	Origin *domain.ForwardRef
}

func (m *Editor) Forward(ctx context.Context, tenant, user string, key store.MsgKey) (Forwarded, error) {
	src, err := m.forwardSource(ctx, tenant, user, key)
	if err != nil {
		return Forwarded{}, err
	}
	return Forwarded{Text: src.Text, Origin: forwardOrigin(key, src)}, nil
}

func (m *Editor) forwardSource(ctx context.Context, tenant, user string, key store.MsgKey) (domain.Message, error) {
	grant, err := m.d.Access.Admit(ctx, access.ForwardMessage, tenant, user, key.Room)
	if err != nil {
		return domain.Message{}, err
	}
	src, err := m.find(ctx, key)
	if err != nil {
		return domain.Message{}, err
	}
	if err := m.forwardable(ctx, grant.Member, key, src); err != nil {
		return domain.Message{}, err
	}
	grant.Author, grant.Kind = src.From, src.Kind
	return src, m.d.Access.Allow(ctx, grant)
}

func (m *Editor) forwardable(ctx context.Context, member domain.Member, key store.MsgKey, src domain.Message) error {
	if clearedFor(member, src.CreatedAt) {
		return domain.ErrMessageNotFound
	}
	_, hidden, err := m.d.Hidden.Get(ctx, member.User, key)
	switch {
	case err != nil:
		return err
	case hidden:
		return domain.ErrMessageNotFound
	case src.Deleted:
		return domain.ErrMessageDeleted
	default:
		return nil
	}
}

func clearedFor(member domain.Member, createdAt time.Time) bool {
	return !member.ClearedAt.IsZero() && !createdAt.After(member.ClearedAt)
}

func forwardOrigin(key store.MsgKey, src domain.Message) *domain.ForwardRef {
	if src.Forward != nil {
		origin := *src.Forward
		return &origin
	}
	return &domain.ForwardRef{Room: key.Room, Thread: key.Thread, Seq: key.Seq, Author: src.From, SentAt: src.CreatedAt}
}
