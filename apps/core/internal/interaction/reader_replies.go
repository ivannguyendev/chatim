package interaction

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ReplyPage struct {
	Messages []message.Item
	Next     uint64
}

func (r *Reader) Replies(ctx context.Context, tenant, user string, parent store.MsgKey, after uint64, limit int) (ReplyPage, error) {
	if err := parent.Validate(); err != nil {
		return ReplyPage{}, err
	}
	n, err := domain.PageLimit(limit)
	if err != nil {
		return ReplyPage{}, err
	}
	grant, err := r.d.Access.Admit(ctx, access.ReadReplies, tenant, user, parent.Room)
	if err != nil {
		return ReplyPage{}, err
	}
	found, err := r.d.Messages.Find(ctx, parent.Room, []store.MsgKey{parent})
	if err != nil {
		return ReplyPage{}, err
	}
	if len(found) == 0 {
		return ReplyPage{}, domain.ErrMessageNotFound
	}
	grant.Author, grant.Kind = found[0].From, found[0].Kind
	if err := r.d.Access.Allow(ctx, grant); err != nil {
		return ReplyPage{}, err
	}
	replies, err := r.d.Interactions.Replies(ctx, parent, after, n)
	if err != nil {
		return ReplyPage{}, err
	}
	msgs, err := r.replyMessages(ctx, user, grant, replies)
	if err != nil {
		return ReplyPage{}, err
	}
	items, err := r.d.Views.Mine(ctx, user, msgs)
	if err != nil {
		return ReplyPage{}, err
	}
	page := ReplyPage{Messages: items}
	if len(replies) == n {
		page.Next = replies[len(replies)-1].Seq
	}
	return page, nil
}

func (r *Reader) replyMessages(ctx context.Context, user string, grant access.Request, replies []domain.Reply) ([]domain.Message, error) {
	if len(replies) == 0 {
		return nil, nil
	}
	keys := make([]store.MsgKey, len(replies))
	for i, rp := range replies {
		keys[i] = store.ReplyKeyOf(rp)
	}
	found, err := r.d.Messages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b domain.Message) int { return cmp.Compare(a.Seq, b.Seq) })
	return r.d.Views.Viewed(ctx, user, grant, grant.Room.ID, 0, found)
}
