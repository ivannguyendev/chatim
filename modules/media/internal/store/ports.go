package store

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type Uploads interface {
	Insert(ctx context.Context, u domain.Upload) error
	Get(ctx context.Context, key string) (domain.Upload, error)
	Transition(ctx context.Context, key string, from domain.State, fromVer int64, to domain.State, hash string) (domain.Upload, error)
	ListOwned(ctx context.Context, tenant, user, before string, kinds domain.Kinds, limit int) ([]domain.Upload, error)
}
