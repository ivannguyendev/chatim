package mutate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errRemoveSelf = fmt.Errorf("%w: target is the caller; leave the room instead", apperr.ErrInvalidArgument)

type memberOp struct {
	action       access.Action
	owner        ownership.Action
	tenant       string
	caller       string
	room         uint64
	target       string
	role         domain.Role
	priority     int32
	requestID    string
	at           time.Time
	removes      bool
	setsPriority bool
}

func (m *Mutator) RemoveMember(ctx context.Context, c RemoveMemberCmd) (MemberResult, error) {
	if c.Target == c.User {
		return MemberResult{}, errRemoveSelf
	}
	return m.change(ctx, memberOp{action: access.RemoveMember, owner: ownership.Remove, tenant: c.Tenant, caller: c.User, room: c.Room, target: c.Target, removes: true})
}

func (m *Mutator) LeaveRoom(ctx context.Context, c LeaveRoomCmd) (MemberResult, error) {
	return m.change(ctx, memberOp{action: access.LeaveRoom, owner: ownership.Leave, tenant: c.Tenant, caller: c.User, room: c.Room, target: c.User, removes: true})
}

func (m *Mutator) ChangeMemberRole(ctx context.Context, c ChangeRoleCmd) (MemberResult, error) {
	if _, err := domain.ParseRole(string(c.Role)); err != nil {
		return MemberResult{}, err
	}
	return m.change(ctx, memberOp{action: access.ChangeMemberRole, owner: ownership.ChangeRole, tenant: c.Tenant, caller: c.User, room: c.Room, target: c.Target, role: c.Role})
}

func (m *Mutator) SetMemberPriority(ctx context.Context, c SetPriorityCmd) (MemberResult, error) {
	return m.change(ctx, memberOp{action: access.SetMemberPriority, tenant: c.Tenant, caller: c.User, room: c.Room, target: c.Target, priority: c.Priority, setsPriority: true})
}

func (m *Mutator) change(ctx context.Context, op memberOp) (MemberResult, error) {
	if err := domain.ValidUser(op.target); err != nil {
		return MemberResult{}, err
	}
	req, err := m.memberGrant(ctx, op.action, op.tenant, op.caller, op.room)
	if op.owner == ownership.Leave && errors.Is(err, domain.ErrNotMember) {
		return m.leftAlready(ctx, op)
	}
	if err != nil {
		return MemberResult{}, err
	}
	op.requestID, op.at = m.d.NewRequestID(), m.now()
	doc, found := req.Member, true
	if op.target != op.caller {
		if doc, found, err = m.memberDoc(ctx, op.room, op.target); err != nil {
			return MemberResult{}, err
		}
	}
	req.Target, req.Role = policyTarget(op, doc, found), op.role
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return MemberResult{}, err
	}
	if res, done, err := op.settled(doc, found); done {
		return res, err
	}
	if !op.setsPriority && ownership.Affects(doc, op.owner, op.role) {
		return m.ownerPath(ctx, op, req)
	}
	return m.plainPath(ctx, op, req.Room, doc)
}

func (m *Mutator) plainPath(ctx context.Context, op memberOp, r domain.Room, doc domain.Member) (MemberResult, error) {
	next := op.next(doc)
	delta := store.MemberCountDelta([]store.MemberWrite{{Cur: doc, Next: next}})
	timer, err := m.maybeArm(ctx, r.ID, delta)
	if err != nil {
		return MemberResult{}, err
	}
	ok, err := m.d.Members.ApplyMember(ctx, doc, next)
	if err != nil {
		m.d.Forget.ForgetMembers(r.ID)
		return MemberResult{}, err
	}
	settle, done := settling(ctx)
	defer done()
	if !ok {
		if delta != 0 {
			m.d.Timers.Disarm(settle, timer)
		}
		return MemberResult{}, domain.ErrRetryLater
	}
	m.d.Forget.ForgetMembers(r.ID)
	var count *chatimv1.Event
	if delta != 0 {
		count = m.settleCount(settle, r, timer, delta, op.caller, op.at)
	}
	m.announce(r, []domain.Member{next}, count)
	return changed(next, doc, ""), nil
}

func (m *Mutator) leftAlready(ctx context.Context, op memberOp) (MemberResult, error) {
	doc, found, err := m.memberDoc(ctx, op.room, op.caller)
	switch {
	case err != nil:
		return MemberResult{}, err
	case !found:
		return MemberResult{}, domain.ErrNotMember
	}
	return unchanged(doc), nil
}

func (m *Mutator) memberDoc(ctx context.Context, room uint64, user string) (domain.Member, bool, error) {
	docs, err := m.d.Members.MembersOf(ctx, room, []string{user})
	if err != nil || len(docs) == 0 {
		return domain.Member{}, false, err
	}
	return docs[0], true, nil
}

func (op memberOp) settled(doc domain.Member, found bool) (MemberResult, bool, error) {
	switch {
	case !found, !doc.Active() && !op.removes:
		return MemberResult{}, true, domain.ErrMemberNotFound
	case !doc.Active(),
		op.owner == ownership.ChangeRole && doc.Role == op.role,
		op.setsPriority && doc.Priority == op.priority:
		return unchanged(doc), true, nil
	}
	return MemberResult{}, false, nil
}

func (op memberOp) next(doc domain.Member) domain.Member {
	role, state, priority := doc.Role, domain.MemberActive, doc.Priority
	switch {
	case op.removes:
		state = domain.MemberRemoved
	case op.setsPriority:
		priority = op.priority
	default:
		role = op.role
	}
	return doc.Next(role, state, priority, op.requestID, op.caller, op.at)
}

func policyTarget(op memberOp, doc domain.Member, found bool) domain.Member {
	if found && doc.Active() {
		return doc
	}
	return domain.Member{Room: op.room, Tenant: op.tenant, User: op.target, Role: domain.RoleMember}
}

func unchanged(doc domain.Member) MemberResult {
	return MemberResult{Member: doc, PreviousRole: doc.Role, PreviousPriority: doc.Priority}
}

func changed(next, cur domain.Member, successor string) MemberResult {
	return MemberResult{Member: next, Changed: true, Successor: successor, PreviousRole: cur.Role, PreviousPriority: cur.Priority}
}
