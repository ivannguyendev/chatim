package mutate

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	errBadRequestID = fmt.Errorf("%w: request_id", apperr.ErrInvalidArgument)
	errNoUsers      = fmt.Errorf("%w: users", apperr.ErrInvalidArgument)
)

func (m *Mutator) AddMembers(ctx context.Context, c AddMembersCmd) ([]domain.Member, error) {
	users, err := m.joiners(c)
	if err != nil {
		return nil, err
	}
	req, err := m.memberGrant(ctx, access.AddMembers, c.Tenant, c.User, c.Room)
	if err != nil {
		return nil, err
	}
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return nil, err
	}
	key := dedupe.RequestKey(c.Room, c.User, c.RequestID)
	status, err := m.d.Requests.Begin(ctx, key)
	switch {
	case err != nil:
		return nil, err
	case status == dedupe.RequestBusy:
		return nil, domain.ErrRetryLater
	case status == dedupe.RequestDone:
		docs, err := m.d.Members.MembersOf(ctx, c.Room, users)
		if err != nil {
			return nil, err
		}
		return addedBy(docs, c.RequestID, c.User), nil
	}
	return m.join(ctx, req.Room, c, users, key)
}

func (m *Mutator) join(ctx context.Context, r domain.Room, c AddMembersCmd, users []string, key dedupe.Key) ([]domain.Member, error) {
	timer, err := m.armCount(ctx, r.ID)
	if err != nil {
		m.cancelRequest(ctx, key)
		return nil, err
	}
	now := m.now()
	j := domain.Join{Room: r.ID, Tenant: c.Tenant, RequestID: c.RequestID, By: c.User, At: now}
	res, err := m.write(ctx, j, users)
	settle, done := settling(ctx)
	defer done()
	if err != nil {
		m.d.Forget.ForgetMembers(r.ID)
		m.d.Requests.Cancel(settle, key)
		return nil, err
	}
	added := addedBy(res.Members, c.RequestID, c.User)
	if len(added) > 0 || res.Changed > 0 {
		m.d.Forget.ForgetMembers(r.ID)
	}
	count := m.settleCount(settle, r, timer, res.Changed, c.User, now)
	m.announce(r, added, count)
	m.d.Requests.Finish(settle, key, dedupe.Record{Seq: uint64(len(users)), CreatedAt: now})
	return added, nil
}

func (m *Mutator) cancelRequest(ctx context.Context, key dedupe.Key) {
	settle, done := settling(ctx)
	defer done()
	m.d.Requests.Cancel(settle, key)
}

func (m *Mutator) write(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	last, err := m.d.Messages.Last(ctx, j.Room, 0)
	if err != nil {
		return store.JoinResult{}, err
	}
	j.ReadSeq = last
	return m.d.Members.AddMembers(ctx, j, users)
}

func (m *Mutator) joiners(c AddMembersCmd) ([]string, error) {
	if domain.ValidCID(c.RequestID) != nil {
		return nil, errBadRequestID
	}
	limit := m.d.Limits.MemberBatch
	seen := make(map[string]struct{}, min(len(c.Users), limit+1))
	users := make([]string, 0, min(len(c.Users), limit))
	for _, u := range c.Users {
		if err := domain.ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		if len(users) == limit {
			return nil, domain.ErrTooManyMembers
		}
		seen[u] = struct{}{}
		users = append(users, u)
	}
	if len(users) == 0 {
		return nil, errNoUsers
	}
	return users, nil
}

func (m *Mutator) memberGrant(ctx context.Context, action access.Action, tenant, user string, room uint64) (access.Request, error) {
	req, err := m.d.Access.Admit(ctx, action, tenant, user, room)
	if err != nil {
		return access.Request{}, err
	}
	if req.Room.Type == domain.RoomDM {
		return access.Request{}, domain.ErrDirectRoom
	}
	return req, nil
}

func addedBy(docs []domain.Member, requestID, by string) []domain.Member {
	out := make([]domain.Member, 0, len(docs))
	for _, d := range docs {
		if domain.AddedBy(d, requestID, by) {
			out = append(out, d)
		}
	}
	return out
}
