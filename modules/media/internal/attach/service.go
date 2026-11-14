package attach

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type Config struct {
	Thumbnails domain.Thumbnails
}

type Deps struct {
	Chat    Chat
	Uploads Uploads
	Files   store.MessageFiles
	Avatars store.RoomAvatars
	Objects Objects
	Log     *slog.Logger
	Now     func() time.Time
}

type Service struct {
	cfg   Config
	deps  Deps
	stats *Stats
}

func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Chat == nil || deps.Uploads == nil || deps.Files == nil || deps.Avatars == nil || deps.Objects == nil {
		return nil, fmt.Errorf("%w: attach needs chat, uploads, message files, room avatars and objects", apperr.ErrInvalidArgument)
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{cfg: cfg, deps: deps, stats: &Stats{}}, nil
}

func (s *Service) Stats() *Stats { return s.stats }

func (s *Service) Sync(ctx context.Context, kind Kind, ev Event) error {
	switch kind {
	case Deleted:
		return s.release(ctx, ev.Tenant, ev.Ref.Key())
	case Created:
		if !ev.HasFiles {
			return nil
		}
		return s.created(ctx, ev)
	case RoomUpdated:
		return s.roomUpdated(ctx, ev)
	}
	return fmt.Errorf("%w: unknown message event kind %d", apperr.ErrInvalidArgument, kind)
}

func (s *Service) created(ctx context.Context, ev Event) error {
	state, err := s.deps.Chat.MessageFiles(ctx, ev.Ref)
	if err != nil {
		return err
	}
	switch {
	case !state.Found:
		s.stats.Drop(ReasonNoMessage)
		return ErrDropped
	case state.Tenant != ev.Tenant:
		s.stats.Drop(ReasonTenant)
		return ErrDropped
	case state.Deleted:
		return s.release(ctx, ev.Tenant, ev.Ref.Key())
	case len(state.Keys) == 0:
		return nil
	}
	return s.attach(ctx, ev, state)
}

func (s *Service) release(ctx context.Context, tenant, messageKey string) error {
	keys, err := s.deps.Files.Active(ctx, tenant, messageKey)
	if err != nil || len(keys) == 0 {
		return err
	}
	used, err := s.deps.Files.StillUsed(ctx, keys, messageKey)
	if err != nil {
		return err
	}
	var idle []string
	for _, k := range keys {
		if !used[k] {
			idle = append(idle, k)
		}
	}
	now := s.deps.Now().UTC()
	if len(idle) > 0 {
		if err := s.deps.Uploads.Retire(ctx, idle, now); err != nil {
			return err
		}
	}
	return s.deps.Files.Release(ctx, tenant, messageKey, now)
}
