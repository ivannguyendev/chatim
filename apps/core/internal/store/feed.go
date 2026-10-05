package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrFeedHistoryLost = fmt.Errorf("change feed history lost: %w", apperr.ErrUnavailable)
	ErrFeedBusy        = fmt.Errorf("change feed held by another consumer: %w", apperr.ErrUnavailable)
	ErrCorruptChange   = fmt.Errorf("change feed returned a corrupt change: %w", apperr.ErrFailedPrecondition)
)

type Position []byte

type ChangeKind uint8

const (
	MessageInserted ChangeKind = iota + 1
	RoomInserted
)

type Change struct {
	Kind        ChangeKind
	Msg         domain.Message
	Room        domain.Room
	CommittedAt time.Time
	Position    Position
}

type ChangeFeed interface {
	Open(ctx context.Context) (Cursor, error)
	Forget(ctx context.Context) error
}

type Cursor interface {
	Next(ctx context.Context) (Change, error)
	Confirm(ctx context.Context, pos Position) error
	Close(ctx context.Context) error
}
