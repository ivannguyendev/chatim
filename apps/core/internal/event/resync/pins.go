package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var ErrPinPageFull = errors.New("resync: one instant holds more pin actions than a pin page")

type Pins interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error)
}

func (s *scanner) pins(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.PinAction]{
		name:    "pin actions",
		limit:   store.MaxPinScan,
		full:    ErrPinPageFull,
		between: s.deps.Pins.Between,
		at:      func(a domain.PinAction) time.Time { return a.At },
		marks: func(a domain.PinAction) []mark {
			return []mark{{store.StateTarget{Collection: store.StatePins, Bit: store.BitPinFact, Room: a.Room, Seq: a.PV}, &s.rep.Pins}}
		},
	})
}
