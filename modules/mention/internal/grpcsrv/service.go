package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/groups"
	"github.com/ivannguyendev/chatim/modules/mention/internal/roomgroups"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MaxSetItems = 1000

var passedThrough = []error{
	apperr.ErrNotFound, apperr.ErrPermissionDenied, apperr.ErrFailedPrecondition,
	apperr.ErrUnavailable, apperr.ErrUnauthenticated, apperr.ErrResourceExhausted,
	context.Canceled, context.DeadlineExceeded,
}

type CoreReader interface {
	RoomsOf(ctx context.Context, tenant, user string) ([]domain.MemberRoom, error)
	SentTimes(ctx context.Context, keys []domain.MessageKey) (map[domain.MessageKey]time.Time, error)
	VisibleMessages(ctx context.Context, tenant, user string, keys []domain.MessageKey) ([]*chatimv1.Message, error)
	Authorize(ctx context.Context, tenant, user string, room uint64, action domain.Action) error
	RoomMemberCount(ctx context.Context, room uint64) (int64, error)
}

type Deps struct {
	Core       CoreReader
	Mentions   store.Mentions
	Rooms      store.Rooms
	UserGroups store.UserGroups
	Groups     store.Groups
	Catalog    *groups.Catalog
	RoomGroups *roomgroups.Sets
	HideRead   []string
}

type Service struct {
	chatimv1.UnimplementedMentionServiceServer
	core       CoreReader
	mentions   store.Mentions
	rooms      store.Rooms
	userGroups store.UserGroups
	groups     store.Groups
	catalog    *groups.Catalog
	roomGroups *roomgroups.Sets
	hideRead   map[string]struct{}
}

func New(d Deps) (*Service, error) {
	if d.Core == nil || d.Mentions == nil || d.Rooms == nil || d.UserGroups == nil || d.Groups == nil || d.Catalog == nil || d.RoomGroups == nil {
		return nil, fmt.Errorf("%w: mention service needs core, mentions, rooms, user groups, groups, a catalog and room groups", apperr.ErrInvalidArgument)
	}
	hide := make(map[string]struct{}, len(d.HideRead))
	for _, tenant := range d.HideRead {
		hide[tenant] = struct{}{}
	}
	return &Service{
		core: d.Core, mentions: d.Mentions, rooms: d.Rooms, userGroups: d.UserGroups, groups: d.Groups,
		catalog: d.Catalog, roomGroups: d.RoomGroups, hideRead: hide,
	}, nil
}

func dependencyErr(op string, err error) error {
	if err == nil {
		return nil
	}
	for _, kept := range passedThrough {
		if errors.Is(err, kept) {
			return fmt.Errorf("%s: %w", op, err)
		}
	}
	return fmt.Errorf("%s: %w: %s", op, apperr.ErrUnavailable, err.Error())
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
}
