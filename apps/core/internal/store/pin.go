package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxPinScan = 1000

var (
	ErrPinExists   = fmt.Errorf("pin version %w", apperr.ErrAlreadyExists)
	ErrPinNotFound = fmt.Errorf("pin action %w", apperr.ErrNotFound)
)

type Pins interface {
	Append(ctx context.Context, a domain.PinAction) error
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
	After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error)
}

type PinProjector interface {
	PinState(ctx context.Context, room uint64) (domain.PinState, error)
	ApplyPins(ctx context.Context, room, base uint64, s domain.PinState) (bool, error)
}

func ValidatePinAction(a domain.PinAction) error {
	switch {
	case a.Room == 0:
		return invalid("room")
	case a.PV == 0 || a.PV > math.MaxInt64:
		return invalid("pin version")
	case a.Op != domain.PinOpPin && a.Op != domain.PinOpUnpin:
		return invalid("pin op")
	case a.Seq == 0:
		return invalid("seq")
	default:
		return domain.ValidUser(a.By)
	}
}
