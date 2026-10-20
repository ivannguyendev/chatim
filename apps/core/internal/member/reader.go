package member

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const defaultMemberPage = 500

var (
	errMissingReaderDeps = fmt.Errorf("%w: member reader needs access and members", apperr.ErrInvalidArgument)
	errMemberLimit       = fmt.Errorf("%w: limit", apperr.ErrInvalidArgument)
)

type MemberLister interface {
	ListMembers(ctx context.Context, room uint64, after store.MemberCursor, limit int) ([]domain.Member, error)
}

type ReaderDeps struct {
	Access  *access.Checker
	Members MemberLister
}

type MemberPage struct {
	Members []domain.Member
	Next    *domain.Member
}

type Reader struct {
	d ReaderDeps
}

func NewReader(d ReaderDeps) (*Reader, error) {
	if d.Access == nil || d.Members == nil {
		return nil, errMissingReaderDeps
	}
	return &Reader{d: d}, nil
}

func (r *Reader) ListMembers(ctx context.Context, tenant, user string, room uint64, after store.MemberCursor, limit uint32) (MemberPage, error) {
	n, err := memberPageLimit(limit)
	if err != nil {
		return MemberPage{}, err
	}
	if _, err := r.d.Access.Authorize(ctx, access.ReadMembers, tenant, user, room); err != nil {
		return MemberPage{}, err
	}
	members, err := r.d.Members.ListMembers(ctx, room, after, n)
	if err != nil {
		return MemberPage{}, err
	}
	page := MemberPage{Members: members}
	if len(members) == n {
		page.Next = &members[len(members)-1]
	}
	return page, nil
}

func memberPageLimit(n uint32) (int, error) {
	switch {
	case n == 0:
		return defaultMemberPage, nil
	case n > store.MaxMemberPage:
		return 0, errMemberLimit
	default:
		return int(n), nil
	}
}
