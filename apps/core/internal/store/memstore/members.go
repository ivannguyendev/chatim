package memstore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	_ store.MemberWriter = (*Rooms)(nil)
	_ store.MemberReader = (*Rooms)(nil)
)

func (s *Rooms) AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	if err := ctx.Err(); err != nil {
		return store.JoinResult{}, err
	}
	if err := store.ValidateJoin(j, users); err != nil {
		return store.JoinResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := store.JoinResult{Members: make([]domain.Member, len(users))}
	for i, u := range users {
		k := memberKey{j.Room, u}
		cur := s.members[k]
		if !cur.Active() {
			next := stamped(j.Apply(cur, u))
			next.LastChangeAt = later(cur.LastChangeAt, next.LastChangeAt)
			s.members[k] = next
			s.logMemberLocked(store.MemberChanged, next)
			out.Changed++
		}
		out.Members[i] = s.members[k]
	}
	return out, nil
}

func (s *Rooms) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateMemberChange(cur, next); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{cur.Room, cur.User}
	stored, ok := s.members[k]
	if !ok || stored.Ver != cur.Ver {
		return false, nil
	}
	s.members[k] = withMembership(stored, next)
	s.logMemberLocked(store.MemberChanged, s.members[k])
	return true, nil
}

func (s *Rooms) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.membersOfLocked(room, users), nil
}

func (s *Rooms) membersOfLocked(room uint64, users []string) []domain.Member {
	out := make([]domain.Member, 0, len(users))
	for _, u := range users {
		if m, ok := s.members[memberKey{room, u}]; ok {
			out = append(out, m)
		}
	}
	return out
}

func (s *Rooms) MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Member{}
	for k, m := range s.members {
		if k.room == room && !m.LastChangeAt.Before(from) && !m.LastChangeAt.After(to) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, byChangeThenKey)
	return out[:min(len(out), limit)], nil
}

func byChangeThenKey(a, b domain.Member) int {
	return cmp.Or(a.LastChangeAt.Compare(b.LastChangeAt), cmp.Compare(len(a.User), len(b.User)), cmp.Compare(a.User, b.User))
}

func withMembership(stored, next domain.Member) domain.Member {
	stored.Role, stored.State, stored.Priority, stored.Ver = next.Role, next.State, next.Priority, next.Ver
	stored.PreviousRole, stored.PreviousState, stored.PreviousPriority = next.PreviousRole, next.PreviousState, next.PreviousPriority
	stored.RequestID, stored.UpdatedBy = next.RequestID, next.UpdatedBy
	stored.UpdatedAt, stored.LastChangeAt = toMillis(next.UpdatedAt), later(stored.LastChangeAt, toMillis(next.LastChangeAt))
	return stored
}

func stamped(m domain.Member) domain.Member {
	m.JoinedAt, m.ClearedAt = toMillis(m.JoinedAt), toMillis(m.ClearedAt)
	m.UpdatedAt, m.LastChangeAt = toMillis(m.UpdatedAt), toMillis(m.LastChangeAt)
	return m
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func toMillis(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.UnixMilli(t.UnixMilli()).UTC()
}
