package ownership

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	Leave      Action = "leave"
	Remove     Action = "remove"
	ChangeRole Action = "change_role"
)

var errUnknownAction = fmt.Errorf("%w: ownership action", apperr.ErrInvalidArgument)

type Request struct {
	Action    Action
	Caller    string
	Target    string
	Role      domain.Role
	RequestID string
	At        time.Time
	Allow     func(caller, target domain.Member) error
}

func Affects(target domain.Member, a Action, role domain.Role) bool {
	return activeOwner(target) || (a == ChangeRole && role == domain.RoleOwner)
}

func Plan(r Request, v store.OwnerView) ([]store.MemberWrite, error) {
	if r.Action != Leave && r.Action != Remove && r.Action != ChangeRole {
		return nil, errUnknownAction
	}
	target := r.Target
	if r.Action == Leave {
		target = r.Caller
	}
	caller, ok := find(v.Docs, r.Caller)
	if r.Action == Leave && ok && !caller.Active() {
		return nil, nil
	}
	if !ok || !caller.Active() {
		return nil, domain.ErrNotMember
	}
	doc, found := find(v.Docs, target)
	if err := r.Allow(caller, policyTarget(doc, found, target)); err != nil {
		return nil, err
	}
	switch {
	case !found:
		return nil, domain.ErrMemberNotFound
	case !doc.Active() && r.Action == ChangeRole:
		return nil, domain.ErrMemberNotFound
	case !doc.Active(), r.Action == ChangeRole && doc.Role == r.Role:
		return nil, nil
	}
	out := store.MemberWrite{Cur: doc, Next: r.next(doc)}
	if !activeOwner(doc) || activeOwner(out.Next) || otherOwner(v.Owners, doc.User) {
		return []store.MemberWrite{out}, nil
	}
	if r.Action == ChangeRole {
		return nil, domain.ErrLastOwner
	}
	heir, ok := Successor(v.Candidates)
	if !ok {
		return []store.MemberWrite{out}, nil
	}
	up := store.MemberWrite{Cur: heir, Next: heir.Next(domain.RoleOwner, domain.MemberActive, heir.Priority, r.RequestID, r.Caller, r.At)}
	return []store.MemberWrite{up, out}, nil
}

func (r Request) next(doc domain.Member) domain.Member {
	if r.Action == ChangeRole {
		return doc.Next(r.Role, domain.MemberActive, doc.Priority, r.RequestID, r.Caller, r.At)
	}
	return doc.Next(doc.Role, domain.MemberRemoved, doc.Priority, r.RequestID, r.Caller, r.At)
}

func policyTarget(doc domain.Member, found bool, user string) domain.Member {
	if found && doc.Active() {
		return doc
	}
	return domain.Member{User: user, Role: domain.RoleMember}
}

func activeOwner(m domain.Member) bool { return m.Active() && m.Role == domain.RoleOwner }

func otherOwner(owners []domain.Member, user string) bool {
	for _, o := range owners {
		if o.User != user && activeOwner(o) {
			return true
		}
	}
	return false
}

func find(docs []domain.Member, user string) (domain.Member, bool) {
	for _, m := range docs {
		if m.User == user {
			return m, true
		}
	}
	return domain.Member{}, false
}
