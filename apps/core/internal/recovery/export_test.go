package recovery

import (
	"context"
	"time"
)

func (s *Sweeper) SetClock(now func() time.Time) { s.now = now }

func (s *Sweeper) Pass(ctx context.Context, slots []uint16) { s.sweep(ctx, slots) }
