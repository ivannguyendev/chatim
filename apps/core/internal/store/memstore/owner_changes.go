package memstore

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.OwnerChanges = (*Rooms)(nil)

const maxOwnersInView = 2

func (s *Rooms) ChangeOwners(ctx context.Context, room uint64, users []string, decide store.OwnerDecision) (store.OwnerResult, error) {
	if err := ctx.Err(); err != nil {
		return store.OwnerResult{}, err
	}
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return store.OwnerResult{}, err
	}
	view, err := s.ownerView(room, users)
	if err != nil {
		return store.OwnerResult{}, err
	}
	writes, err := decide(view)
	if err != nil || len(writes) == 0 {
		return store.OwnerResult{}, err
	}
	if err := store.ValidateOwnerWrites(room, writes); err != nil {
		return store.OwnerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return store.OwnerResult{}, err
	}
	return s.commitOwners(room, view.OwnersVer, writes)
}

func (s *Rooms) ownerView(room uint64, users []string) (store.OwnerView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.rooms[room]; !ok {
		return store.OwnerView{}, domain.ErrRoomNotFound
	}
	var owners []domain.Member
	var admin, plain *domain.Member
	for k, m := range s.members {
		if k.room != room || !m.Active() {
			continue
		}
		switch m.Role {
		case domain.RoleOwner:
			owners = append(owners, m)
		case domain.RoleAdmin:
			admin = aheadOf(admin, m)
		case domain.RoleMember:
			plain = aheadOf(plain, m)
		}
	}
	slices.SortFunc(owners, byJoinThenUser)
	v := store.OwnerView{OwnersVer: s.ownersVer[room], Docs: s.membersOfLocked(room, users), Owners: owners[:min(len(owners), maxOwnersInView)]}
	for _, c := range []*domain.Member{admin, plain} {
		if c != nil {
			v.Candidates = append(v.Candidates, *c)
		}
	}
	return v, nil
}

func (s *Rooms) commitOwners(room, ownersVer uint64, writes []store.MemberWrite) (store.OwnerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || s.ownersVer[room] != ownersVer {
		return store.OwnerResult{}, domain.ErrRetryLater
	}
	for _, w := range writes {
		if cur, ok := s.members[memberKey{room, w.Cur.User}]; !ok || cur.Ver != w.Cur.Ver {
			return store.OwnerResult{}, domain.ErrRetryLater
		}
	}
	res := store.OwnerResult{Written: make([]domain.Member, 0, len(writes))}
	for _, w := range writes {
		k := memberKey{room, w.Cur.User}
		s.members[k] = withMembership(s.members[k], w.Next)
		res.Written = append(res.Written, w.Next)
	}
	s.ownersVer[room]++
	if delta := store.MemberCountDelta(writes); delta != 0 {
		r.MemberCount += delta
		r.MemberCountVer++
		s.rooms[room] = r
		res.Count, res.CountChanged = domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer}, true
	}
	return res, nil
}

func aheadOf(best *domain.Member, m domain.Member) *domain.Member {
	if best == nil || byCandidateOrder(m, *best) < 0 {
		return &m
	}
	return best
}

func byCandidateOrder(a, b domain.Member) int {
	return cmp.Or(cmp.Compare(b.Priority, a.Priority), a.JoinedAt.Compare(b.JoinedAt), cmp.Compare(a.User, b.User))
}

func byJoinThenUser(a, b domain.Member) int {
	return cmp.Or(a.JoinedAt.Compare(b.JoinedAt), cmp.Compare(a.User, b.User))
}
