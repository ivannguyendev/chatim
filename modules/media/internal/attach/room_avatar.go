package attach

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func (s *Service) roomUpdated(ctx context.Context, ev Event) error {
	state, err := s.deps.Chat.RoomAvatars(ctx, ev.Ref.Room)
	if err != nil {
		return err
	}
	switch {
	case !state.Found:
		s.stats.Drop(ReasonNoRoom)
		return ErrDropped
	case state.Tenant != ev.Tenant:
		s.stats.Drop(ReasonTenant)
		return ErrDropped
	case !state.Group:
		return nil
	}
	doc, found, err := s.deps.Avatars.Get(ctx, ev.Ref.Room)
	if err != nil || (found && doc.InfoVer >= state.InfoVer) {
		return err
	}
	next := shownAvatar(state)
	if next == "" && !found {
		return nil
	}
	if next != "" {
		if err := s.keepAvatar(ctx, state.Tenant, next); err != nil {
			return err
		}
	}
	now := s.deps.Now().UTC()
	record := domain.RoomAvatar{Room: ev.Ref.Room, Tenant: state.Tenant, FileKey: next, InfoVer: state.InfoVer, UpdatedAt: now}
	if err := s.deps.Avatars.Put(ctx, record, doc.InfoVer); err != nil {
		return err
	}
	if doc.FileKey == "" || doc.FileKey == next {
		return nil
	}
	used, err := s.deps.Avatars.InUse(ctx, doc.FileKey)
	if err != nil || used {
		return err
	}
	return s.deps.Uploads.Retire(ctx, []string{doc.FileKey}, now)
}

func shownAvatar(state domain.RoomAvatarState) string {
	if len(state.Keys) == 0 {
		return ""
	}
	info, ok := domain.ParseKey(state.Keys[0])
	if !ok || !info.Public || info.Tenant != state.Tenant {
		return ""
	}
	return state.Keys[0]
}

func (s *Service) keepAvatar(ctx context.Context, tenant, key string) error {
	keys := []string{key}
	uploads, err := s.deps.Uploads.GetMany(ctx, tenant, keys)
	if err != nil {
		return err
	}
	now := s.deps.Now().UTC()
	valid, drops, err := classify(tenant, keys, uploads, now)
	if err != nil {
		return err
	}
	s.countDrops(drops)
	if len(valid) == 0 {
		return nil
	}
	if err := s.deps.Uploads.Unretire(ctx, keys); err != nil {
		return err
	}
	if valid[0].Bucket == domain.BucketCompleted {
		return nil
	}
	return s.move(ctx, valid[0], now)
}
