package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageEditor = (*Messages)(nil)

func (s *Messages) ApplyEdit(ctx context.Context, e domain.Edit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateEdit(e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.lines[timeline{e.Room, e.Thread}]
	i, found := slices.BinarySearchFunc(line, e.Seq, bySeq)
	if !found || line[i].Version >= e.Version {
		return nil
	}
	line[i] = projected(line[i], e)
	return nil
}

func projected(m domain.Message, e domain.Edit) domain.Message {
	m.Version, m.EditedAt, m.Text, m.Deleted = e.Version, e.At, e.Text, false
	if e.Kind == domain.EditDelete {
		m.Text, m.Deleted = "", true
	}
	return m
}
