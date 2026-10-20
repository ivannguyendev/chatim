package message

import (
	"cmp"
	"context"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type HistoryQuery struct {
	store.PageQuery
	Around bool
}

func (r *Reader) History(ctx context.Context, tenant, user string, q HistoryQuery) ([]Item, error) {
	grant, err := r.d.Access.Authorize(ctx, access.ReadHistory, tenant, user, q.Room)
	if err != nil {
		return nil, err
	}
	page, err := r.historyPage(ctx, q)
	if err != nil {
		return nil, err
	}
	return r.items(ctx, user, grant, q.Room, q.Thread, page)
}

func (r *Reader) Messages(ctx context.Context, tenant, user string, room uint64, keys []store.MsgKey) ([]Item, error) {
	grant, err := r.d.Access.Authorize(ctx, access.ReadHistory, tenant, user, room)
	if err != nil {
		return nil, err
	}
	found, err := r.d.Pages.Find(ctx, room, keys)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b domain.Message) int { return cmp.Compare(a.Seq, b.Seq) })
	return r.items(ctx, user, grant, room, 0, found)
}

func (r *Reader) items(ctx context.Context, user string, grant access.Request, room, thread uint64, page []domain.Message) ([]Item, error) {
	viewer, err := r.viewerOf(ctx, user, grant, room, thread, page, quotedSeqs(room, page)...)
	if err != nil {
		return nil, err
	}
	shown := r.view.Apply(viewer, page)
	out := itemsOf(shown)
	if err := r.addReplyPreviews(ctx, viewer, room, shown, out); err != nil {
		return nil, err
	}
	if err := r.addMine(ctx, user, shown, out); err != nil {
		return nil, err
	}
	return out, nil
}

func aroundQueries(q store.PageQuery) (back, ahead store.PageQuery) {
	back, ahead = q, q
	back.Anchor, back.Seq, back.Limit = store.Before, q.Seq+1, (q.Limit+1)/2
	ahead.Anchor, ahead.Limit = store.After, q.Limit-back.Limit
	return back, ahead
}

func (r *Reader) historyPage(ctx context.Context, q HistoryQuery) ([]domain.Message, error) {
	if !q.Around {
		return r.d.Pages.Page(ctx, q.PageQuery)
	}
	back, ahead := aroundQueries(q.PageQuery)
	var older, newer []domain.Message
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		older, err = r.d.Pages.Page(gctx, back)
		return err
	})
	if ahead.Limit > 0 {
		g.Go(func() (err error) {
			newer, err = r.d.Pages.Page(gctx, ahead)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return slices.Concat(older, newer), nil
}
