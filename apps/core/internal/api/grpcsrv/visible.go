package grpcsrv

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type roomThread struct{ room, thread uint64 }

func (s *Service) VisibleMessages(ctx context.Context, tenant, user string, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error) {
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
		if err := s.visibleIn(ctx, tenant, user, g, groups[g], visible); err != nil {
			return nil, err
		}
	}
	return visible, nil
}

func (s *Service) visibleIn(ctx context.Context, tenant, user string, g roomThread, keys []store.MsgKey, visible map[store.MsgKey]domain.Message) error {
	grant, err := s.access.Authorize(ctx, access.ReadHistory, tenant, user, g.room)
	if errors.Is(err, apperr.ErrPermissionDenied) || errors.Is(err, apperr.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	found, err := s.pages.Find(ctx, g.room, keys)
	if err != nil {
		return err
	}
	viewer, err := s.viewerOf(ctx, user, grant, store.PageQuery{Room: g.room, Thread: g.thread}, found)
	if err != nil {
		return err
	}
	for _, m := range s.maskView.Apply(viewer, found) {
		if !m.Deleted && !m.Hidden {
			visible[store.KeyOf(m)] = m
		}
	}
	return nil
}
