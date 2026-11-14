package attach

import (
	"context"
	"time"

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
	kept := false
	if next != "" {
		if kept, err = s.keepAvatar(ctx, state.Tenant, next); err != nil {
			return err
		}
	}
	now := s.deps.Now().UTC()
	record := domain.RoomAvatar{Room: ev.Ref.Room, Tenant: state.Tenant, FileKey: next, InfoVer: state.InfoVer, UpdatedAt: now}
	if err := s.deps.Avatars.Put(ctx, record, doc.InfoVer); err != nil {
		return err
	}
	if kept {
		if err := s.deps.Uploads.Unretire(ctx, []string{next}); err != nil {
			return err
		}
	}
	if doc.FileKey == "" || doc.FileKey == next {
		return nil
	}
	return s.retireUnlessShown(ctx, doc.FileKey, now)
}

func (s *Service) retireUnlessShown(ctx context.Context, key string, now time.Time) error {
	if used, err := s.deps.Avatars.InUse(ctx, key); err != nil || used {
		return err
	}
	if err := s.deps.Uploads.Retire(ctx, []string{key}, now); err != nil {
		return err
	}
	used, err := s.deps.Avatars.InUse(ctx, key)
	if err != nil || !used {
		return err
	}
	return s.deps.Uploads.Unretire(ctx, []string{key})
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

func (s *Service) keepAvatar(ctx context.Context, tenant, key string) (bool, error) {
	keys := []string{key}
	uploads, err := s.deps.Uploads.GetMany(ctx, tenant, keys)
	if err != nil {
		return false, err
	}
	now := s.deps.Now().UTC()
	valid, drops, err := classify(tenant, keys, uploads, now)
	if err != nil {
		return false, err
	}
	s.countDrops(drops)
	if len(valid) == 0 {
		return false, nil
	}
	if err := s.deps.Uploads.Unretire(ctx, keys); err != nil {
		return false, err
	}
	if valid[0].Bucket == domain.BucketCompleted {
		return true, nil
	}
	return true, s.move(ctx, valid[0], now)
}
