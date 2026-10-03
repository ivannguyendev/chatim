package store

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type Messages interface {
	Insert(ctx context.Context, msgs []domain.Message) []Result
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	Page(ctx context.Context, q PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []MsgKey) ([]domain.Message, error)
}

type Rooms interface {
	Create(ctx context.Context, r domain.Room, members []domain.Member) error
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
}
