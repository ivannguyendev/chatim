package memstore

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

var _ store.Groups = (*Groups)(nil)

type Groups struct {
	mu   sync.Mutex
	docs map[tenantKey]domain.Group
}

func NewGroups() *Groups {
	return &Groups{docs: make(map[tenantKey]domain.Group)}
}

func (s *Groups) Set(ctx context.Context, items []domain.Group) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateGroups(items); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := []string{}
	for _, it := range items {
		k := tenantKey{it.Tenant, it.ID}
		cur, ok := s.docs[k]
		if ok && cur.Ver >= it.Ver {
			continue
		}
		s.docs[k] = it
		if !ok || cur.Name != it.Name || cur.Active != it.Active {
			changed = append(changed, it.ID)
		}
	}
	return changed, nil
}

func (s *Groups) All(ctx context.Context, tenant string) ([]domain.Group, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateTenant(tenant); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Group{}
	for k, g := range s.docs {
		if k.tenant == tenant {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b domain.Group) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}
