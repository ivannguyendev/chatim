package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type Uploads interface {
	Insert(ctx context.Context, u domain.Upload) error
	Get(ctx context.Context, key string) (domain.Upload, error)
	Transition(ctx context.Context, key string, from domain.State, fromVer int64, to domain.State, hash string) (domain.Upload, error)
	ListOwned(ctx context.Context, tenant, user, before string, kinds domain.Kinds, limit int) ([]domain.Upload, error)
	GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error)
	MarkMoved(ctx context.Context, key string, ver int64, at time.Time) error
	Retire(ctx context.Context, keys []string, at time.Time) error
	Unretire(ctx context.Context, keys []string) error
}

type MessageFiles interface {
	Attach(ctx context.Context, pairs []domain.MessageFile) error
	Active(ctx context.Context, tenant, messageKey string) ([]string, error)
	Release(ctx context.Context, tenant, messageKey string, at time.Time) error
	StillUsed(ctx context.Context, fileKeys []string) (map[string]bool, error)
}
