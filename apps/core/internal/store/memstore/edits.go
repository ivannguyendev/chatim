package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Edits = (*Edits)(nil)

type Edits struct {
	mu    sync.RWMutex
	facts map[store.MsgKey][]domain.Edit
	log   *Messages
}

func NewEdits() *Edits { return &Edits{facts: make(map[store.MsgKey][]domain.Edit)} }

func (s *Edits) Append(ctx context.Context, e domain.Edit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateEdit(e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := store.EditKeyOf(e)
	line := s.facts[key]
	i, found := slices.BinarySearchFunc(line, e.Version, byVersion)
	if found {
		return fmt.Errorf("append edit v%d of %+v: %w", e.Version, key, store.ErrEditExists)
	}
	s.facts[key] = slices.Insert(line, i, e)
	if s.log != nil {
		s.log.appendFact(logged{kind: store.EditInserted, edit: e})
	}
	return nil
}

func (s *Edits) At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return domain.Edit{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	if i, ok := slices.BinarySearchFunc(line, version, byVersion); ok {
		return line[i], nil
	}
	return domain.Edit{}, fmt.Errorf("edit v%d of %+v: %w", version, key, store.ErrEditNotFound)
}

func (s *Edits) Latest(ctx context.Context, key store.MsgKey) (domain.Edit, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Edit{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	if len(line) == 0 || line[len(line)-1].Version == 0 {
		return domain.Edit{}, false, nil
	}
	return line[len(line)-1], true, nil
}

func (s *Edits) History(ctx context.Context, key store.MsgKey, after uint32, limit int) ([]domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxEditPage); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	i, found := slices.BinarySearchFunc(line, after, byVersion)
	if found {
		i++
	}
	return slices.Clone(line[i:min(len(line), i+limit)]), nil
}

func (s *Edits) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxEditScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Edit{}
	for key, line := range s.facts {
		if key.Room != room {
			continue
		}
		for _, e := range line {
			if !e.At.Before(from) && !e.At.After(to) {
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, byTimeThenKey)
	return out[:min(len(out), limit)], nil
}

func (s *Edits) PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[key]
	for i := range line {
		if line[i].Version <= upTo {
			line[i].Text = ""
		}
	}
	return nil
}

func byVersion(e domain.Edit, v uint32) int { return cmp.Compare(e.Version, v) }

func byTimeThenKey(a, b domain.Edit) int {
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.Version, b.Version))
}
