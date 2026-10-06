package resync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const editPage = store.MaxEditScan

var ErrEditPageFull = errors.New("resync: one instant holds more edits than an edit page")

type Edits interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
}

func (s *scanner) edits(ctx context.Context, room uint64) error {
	from, edge := s.opts.From, map[string]bool{}
	for {
		page, err := s.deps.Edits.Between(ctx, room, from, s.opts.To, editPage)
		if err != nil {
			return fmt.Errorf("edits of room %d from %s: %w", room, from.Format(time.RFC3339Nano), err)
		}
		next, err := s.emitEdits(ctx, page, edge)
		if err != nil || len(page) < editPage {
			return err
		}
		last := page[len(page)-1].At
		if !last.After(from) {
			return fmt.Errorf("%w: room %d at %s", ErrEditPageFull, room, last.Format(time.RFC3339Nano))
		}
		from, edge = last, next
	}
}

func (s *scanner) emitEdits(ctx context.Context, page []domain.Edit, edge map[string]bool) (map[string]bool, error) {
	next := map[string]bool{}
	for _, e := range page {
		rec := work.Record{Kind: store.EditInserted, Room: e.Room, Thread: e.Thread, Seq: e.Seq, Version: e.Version, CommittedAt: e.At}
		id := rec.ID()
		if e.At.Equal(page[len(page)-1].At) {
			next[id] = true
		}
		if edge[id] {
			continue
		}
		if err := s.emit(ctx, rec); err != nil {
			return nil, err
		}
		s.rep.EditRecords++
	}
	return next, nil
}
