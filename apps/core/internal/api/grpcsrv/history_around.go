package grpcsrv

import (
	"context"
	"math"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type historyQuery struct {
	store.PageQuery
	around bool
}

func aroundAnchor(seq uint64) (store.Anchor, uint64, error) {
	if seq == math.MaxUint64 {
		return 0, 0, errAnchorSeq
	}
	return seqAnchor(store.Before, seq)
}

func aroundQueries(q store.PageQuery) (back, ahead store.PageQuery) {
	back, ahead = q, q
	back.Anchor, back.Seq, back.Limit = store.Before, q.Seq+1, (q.Limit+1)/2
	ahead.Anchor, ahead.Limit = store.After, q.Limit-back.Limit
	return back, ahead
}

func (s *Service) historyPage(ctx context.Context, q historyQuery) ([]domain.Message, error) {
	if !q.around {
		return s.pages.Page(ctx, q.PageQuery)
	}
	back, ahead := aroundQueries(q.PageQuery)
	var older, newer []domain.Message
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		older, err = s.pages.Page(gctx, back)
		return err
	})
	if ahead.Limit > 0 {
		g.Go(func() (err error) {
			newer, err = s.pages.Page(gctx, ahead)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return slices.Concat(older, newer), nil
}
