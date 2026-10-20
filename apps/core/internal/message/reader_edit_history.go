package message

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (r *Reader) EditHistory(ctx context.Context, tenant, user string, key store.MsgKey, afterVer uint32, limit int) ([]domain.Edit, error) {
	grant, err := r.d.Access.Admit(ctx, access.ReadEditHistory, tenant, user, key.Room)
	if err != nil {
		return nil, err
	}
	found, err := r.d.Pages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, domain.ErrMessageNotFound
	}
	grant.Author = found[0].From
	if err := r.d.Access.Allow(ctx, grant); err != nil {
		return nil, err
	}
	if found[0].Deleted {
		return nil, nil
	}
	rows, err := r.d.Edits.History(ctx, key, afterVer, limit)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	switch deleting, err := r.deleting(ctx, key, rows, limit); {
	case err != nil:
		return nil, err
	case deleting:
		return nil, nil
	}
	if afterVer == 0 {
		switch row, err := r.d.Edits.At(ctx, key, 0); {
		case err == nil:
			rows = append([]domain.Edit{row}, rows...)
		case !errors.Is(err, store.ErrEditNotFound):
			return nil, err
		}
	}
	return rows, nil
}

func (r *Reader) deleting(ctx context.Context, key store.MsgKey, page []domain.Edit, limit int) (bool, error) {
	last := page[len(page)-1]
	if len(page) == limit {
		latest, ok, err := r.d.Edits.Latest(ctx, key)
		if err != nil || !ok {
			return false, err
		}
		last = latest
	}
	return last.Kind == domain.EditDelete, nil
}
