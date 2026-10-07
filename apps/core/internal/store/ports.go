package store

import (
	"context"
	"time"

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
	TouchActivity(ctx context.Context, acts []Activity) error
	ActiveRooms(ctx context.Context, q ActiveQuery) ([]domain.Room, error)
}

type MessageEditor interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error)
}

type Edits interface {
	Append(ctx context.Context, e domain.Edit) error
	At(ctx context.Context, key MsgKey, version uint32) (domain.Edit, error)
	Latest(ctx context.Context, key MsgKey) (domain.Edit, bool, error)
	History(ctx context.Context, key MsgKey, after uint32, limit int) ([]domain.Edit, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
	PurgeText(ctx context.Context, key MsgKey, upTo uint32) error
}

type Hidden interface {
	Hide(ctx context.Context, user string, key MsgKey, at time.Time) error
	HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.HiddenMessage, error)
}
