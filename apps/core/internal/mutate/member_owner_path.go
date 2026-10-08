package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (m *Mutator) ownerPath(ctx context.Context, op memberOp, req access.Request) (MemberResult, error) {
	users := []string{op.caller}
	if op.target != op.caller {
		users = append(users, op.target)
	}
	var seen domain.Member
	plan := ownership.Request{
		Action: op.owner, Caller: op.caller, Target: op.target, Role: op.role, RequestID: op.requestID, At: op.at,
		Allow: func(caller, target domain.Member) error {
			again := req
			again.Member, again.Target = caller, target
			return m.d.Access.Allow(ctx, again)
		},
	}
	res, err := m.d.Members.ChangeOwners(ctx, op.room, users, func(v store.OwnerView) ([]store.MemberWrite, error) {
		seen = docOf(v.Docs, op.target)
		return ownership.Plan(plan, v)
	})
	if err != nil {
		return MemberResult{}, err
	}
	if len(res.Written) == 0 {
		return unchanged(seen), nil
	}
	m.d.Forget.ForgetMembers(op.room)
	var count *chatimv1.Event
	if res.CountChanged {
		count = pbconv.MemberCountChanged(req.Room, res.Count, op.caller, op.at)
	}
	m.announce(req.Room, res.Written, count)
	return ownerResult(res.Written, op.target), nil
}

func ownerResult(written []domain.Member, target string) MemberResult {
	doc := docOf(written, target)
	prev := doc
	prev.Role, prev.Priority = doc.PreviousRole, doc.PreviousPriority
	successor := ""
	if len(written) > 1 {
		successor = written[0].User
	}
	return changed(doc, prev, successor)
}

func docOf(docs []domain.Member, user string) domain.Member {
	for _, d := range docs {
		if d.User == user {
			return d
		}
	}
	return domain.Member{}
}
