package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
)

type Mentions interface {
	Apply(ctx context.Context, plan domain.Plan) error
	Of(ctx context.Context, tenant string, key domain.MessageKey) ([]domain.Mention, error)
	ListByTarget(ctx context.Context, q TargetMentionQuery) ([]domain.Mention, error)
	ListShared(ctx context.Context, q SharedMentionQuery) ([]domain.Mention, error)
}

type Rooms interface {
	Raise(ctx context.Context, tenant string, room uint64, at time.Time) error
	Times(ctx context.Context, tenant string, rooms []uint64) (map[uint64]time.Time, error)
}

type UserGroups interface {
	Set(ctx context.Context, items []domain.UserGroups) ([]string, error)
	Of(ctx context.Context, tenant string, users []string) (map[string][]string, error)
	MembersOfGroup(ctx context.Context, tenant, group, after string, limit int) ([]string, error)
}

type Groups interface {
	Set(ctx context.Context, items []domain.Group) ([]string, error)
	All(ctx context.Context, tenant string) ([]domain.Group, error)
}
