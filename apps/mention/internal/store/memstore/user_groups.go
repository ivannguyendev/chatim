package memstore

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

var _ store.UserGroups = (*UserGroups)(nil)

type tenantKey struct {
	tenant string
	id     string
}

type UserGroups struct {
	mu   sync.Mutex
	docs map[tenantKey]domain.UserGroups
}

func NewUserGroups() *UserGroups {
	return &UserGroups{docs: make(map[tenantKey]domain.UserGroups)}
}

func (s *UserGroups) Set(ctx context.Context, items []domain.UserGroups) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateUserGroups(items); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := []string{}
	for _, it := range items {
		k := tenantKey{it.Tenant, it.User}
		it.Groups = domain.NormalizeGroups(it.Groups)
		cur, ok := s.docs[k]
		if ok && cur.Ver >= it.Ver {
			continue
		}
		s.docs[k] = it
		if !ok || !slices.Equal(cur.Groups, it.Groups) {
			changed = append(changed, it.User)
		}
	}
	return changed, nil
}

func (s *UserGroups) Of(ctx context.Context, tenant string, users []string) (map[string][]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateUsers(tenant, users); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]string, len(users))
	for _, u := range users {
		if d, ok := s.docs[tenantKey{tenant, u}]; ok {
			out[u] = slices.Clone(d.Groups)
		}
	}
	return out, nil
}

func (s *UserGroups) MembersOfGroup(ctx context.Context, tenant, group, after string, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateGroupMembersQuery(tenant, group, after, limit); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k, d := range s.docs {
		if k.tenant == tenant && k.id > after && slices.Contains(d.Groups, group) {
			out = append(out, k.id)
		}
	}
	slices.SortFunc(out, strings.Compare)
	return out[:min(len(out), limit)], nil
}
