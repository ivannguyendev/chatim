package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Marks interface {
	Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error)
}

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type EditReader interface {
	At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error)
}

type EditApplier interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type TextPurger interface {
	PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error
}
