package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var ErrEditPageFull = errors.New("resync: one instant holds more edits than an edit page")

type Edits interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
}

func (s *scanner) edits(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Edit]{
		name:    "edits",
		limit:   store.MaxEditScan,
		full:    ErrEditPageFull,
		between: s.deps.Edits.Between,
		at:      func(e domain.Edit) time.Time { return e.At },
		marks:   s.editMarks,
	})
}

func (s *scanner) editMarks(e domain.Edit) []mark {
	if e.Version == 0 {
		return nil
	}
	t := store.StateTarget{Collection: store.StateEdits, Bit: store.BitEditFact, Room: e.Room, Thread: e.Thread, Seq: e.Seq, Ver: uint64(e.Version)}
	return []mark{{t, &s.rep.Edits}}
}
