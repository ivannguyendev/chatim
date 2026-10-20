package interaction

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ReactorPage struct {
	Reactors []store.Reactor
	Next     *store.ReactorCursor
}

func (r *Reader) CheckReactorFilter(key store.MsgKey, emoji string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if emoji != "" && !slices.Contains(r.d.Settings.ReactionEmojis(), emoji) {
		return domain.ErrEmojiNotAllowed
	}
	return nil
}

func (r *Reader) Reactors(ctx context.Context, tenant, user string, q store.ReactorQuery, limit int) (ReactorPage, error) {
	n, err := domain.PageLimit(limit)
	if err != nil {
		return ReactorPage{}, err
	}
	q.Limit = n
	visible, err := r.reactionsVisible(ctx, tenant, user, q.Key)
	if err != nil || !visible {
		return ReactorPage{}, err
	}
	reactors, err := r.d.Interactions.Reactors(ctx, q)
	if err != nil {
		return ReactorPage{}, err
	}
	page := ReactorPage{Reactors: reactors}
	if len(reactors) == q.Limit {
		next := q.Cursor(reactors[len(reactors)-1])
		page.Next = &next
	}
	return page, nil
}

func (r *Reader) reactionsVisible(ctx context.Context, tenant, user string, key store.MsgKey) (bool, error) {
	grant, err := r.d.Access.Admit(ctx, access.ReadReactions, tenant, user, key.Room)
	if err != nil {
		return false, err
	}
	found, err := r.d.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return false, err
	}
	if len(found) == 0 {
		return false, domain.ErrMessageNotFound
	}
	grant.Author, grant.Kind = found[0].From, found[0].Kind
	if err := r.d.Access.Allow(ctx, grant); err != nil {
		return false, err
	}
	shown, err := r.d.Views.Masked(ctx, user, grant, key.Room, key.Thread, found)
	if err != nil {
		return false, err
	}
	return message.VisibleToReader(shown[0]), nil
}
