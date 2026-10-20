package message

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type roomThread struct{ room, thread uint64 }

func (r *Reader) VisibleMessages(ctx context.Context, tenant, user string, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error) {
	groups := make(map[roomThread][]store.MsgKey)
	var order []roomThread
	for _, k := range keys {
		g := roomThread{k.Room, k.Thread}
		if _, seen := groups[g]; !seen {
			order = append(order, g)
		}
		groups[g] = append(groups[g], k)
	}
	visible := make(map[store.MsgKey]domain.Message, len(keys))
	for _, g := range order {
		if err := r.visibleIn(ctx, tenant, user, g, groups[g], visible); err != nil {
			return nil, err
		}
	}
	return visible, nil
}

func (r *Reader) visibleIn(ctx context.Context, tenant, user string, g roomThread, keys []store.MsgKey, visible map[store.MsgKey]domain.Message) error {
	grant, err := r.d.Access.Authorize(ctx, access.ReadHistory, tenant, user, g.room)
	if errors.Is(err, apperr.ErrPermissionDenied) || errors.Is(err, apperr.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	found, err := r.d.Pages.Find(ctx, g.room, keys)
	if err != nil {
		return err
	}
	shown, err := r.Masked(ctx, user, grant, g.room, g.thread, found)
	if err != nil {
		return err
	}
	for _, m := range shown {
		if VisibleToReader(m) {
			visible[store.KeyOf(m)] = m
		}
	}
	return nil
}
