package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
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
		record:  editRecord,
		counted: &s.rep.EditRecords,
	})
}

func editRecord(e domain.Edit) work.Record {
	return work.Record{Kind: store.EditInserted, Room: e.Room, Thread: e.Thread, Seq: e.Seq, Version: e.Version, CommittedAt: e.At}
}
