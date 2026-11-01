package api

import (
	"context"
	"path"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
)

type Admin interface {
	CreateRole(ctx context.Context, c auth.Caller, in manage.NewRole) (store.Role, error)
	UpdateRole(ctx context.Context, c auth.Caller, id string, u store.RoleUpdate) (store.Role, error)
	DeleteRole(ctx context.Context, c auth.Caller, id string) (store.RoleRemoval, error)
	GetRole(ctx context.Context, c auth.Caller, id string) (store.Role, error)
	ListRoles(ctx context.Context, c auth.Caller, pageToken string) ([]store.Role, string, error)
	AssignAccountRole(ctx context.Context, c auth.Caller, accountID, roleID string) error
	UnassignAccountRole(ctx context.Context, c auth.Caller, accountID, roleID string) error
	AssignGroupRole(ctx context.Context, c auth.Caller, groupID, roleID string) error
	UnassignGroupRole(ctx context.Context, c auth.Caller, groupID, roleID string) error
	CreateGroup(ctx context.Context, c auth.Caller, in manage.NewGroup) (store.Group, error)
	UpdateGroup(ctx context.Context, c auth.Caller, id string, u store.GroupUpdate) (store.Group, error)
	DeleteGroup(ctx context.Context, c auth.Caller, id string) error
	ListGroups(ctx context.Context, c auth.Caller, pageToken string) ([]store.GroupView, string, error)
	SetAccountGroups(ctx context.Context, c auth.Caller, accountID string, groupIDs []string) error
	GetAccount(ctx context.Context, c auth.Caller, id string) (store.AccountView, error)
	ListAccounts(ctx context.Context, c auth.Caller, pageToken string) ([]store.AccountView, string, error)
}

type adminService struct {
	s *server
}

type adminCall[Req, Resp any] func(ctx context.Context, c auth.Caller, msg *Req) (*Resp, error)

func serveAdmin[Req, Resp any](ctx context.Context, s *server, req *connect.Request[Req], call adminCall[Req, Resp]) (resp *connect.Response[Resp], err error) {
	procedure := req.Spec().Procedure
	defer func() { s.opts.AdminStats.count(path.Base(procedure), err) }()
	claims, done, err := s.enter(req, s.admin)
	if err != nil {
		return nil, err
	}
	defer done()
	out, err := call(ctx, s.callerOf(req, claims), req.Msg)
	if err != nil {
		return nil, s.connectError(ctx, procedure, err)
	}
	return connect.NewResponse(out), nil
}

func roleUpdate(msg *iamv1.UpdateRoleRequest) store.RoleUpdate {
	u := store.RoleUpdate{Name: msg.Name}
	if msg.Permissions != nil {
		list := append([]string{}, msg.Permissions.GetValues()...)
		u.Permissions = &list
	}
	return u
}

func groupUpdate(msg *iamv1.UpdateGroupRequest) store.GroupUpdate {
	return store.GroupUpdate{Name: msg.Name, ParentID: msg.ParentId, Mentionable: msg.Mentionable}
}

func adminRole(r store.Role) *iamv1.Role {
	return &iamv1.Role{
		Id: r.ID, Name: r.Name, Builtin: r.Builtin, Ver: r.Ver, Permissions: r.Permissions,
		CreatedAt: timestamp(r.CreatedAt), UpdatedAt: timestamp(r.UpdatedAt), UpdatedBy: r.UpdatedBy,
	}
}

func adminRoles(list []store.Role) []*iamv1.Role {
	out := make([]*iamv1.Role, 0, len(list))
	for _, r := range list {
		out = append(out, adminRole(r))
	}
	return out
}

func adminGroup(g store.Group, roleIDs []string) *iamv1.AdminGroup {
	return &iamv1.AdminGroup{Id: g.ID, Name: g.Name, ParentId: g.ParentID, Mentionable: g.Mentionable, RoleIds: roleIDs}
}

func adminGroups(list []store.GroupView) []*iamv1.AdminGroup {
	out := make([]*iamv1.AdminGroup, 0, len(list))
	for _, g := range list {
		out = append(out, adminGroup(g.Group, g.RoleIDs))
	}
	return out
}

func adminAccount(a store.AccountView) *iamv1.AdminAccount {
	return &iamv1.AdminAccount{
		Id: a.ID, Username: a.Username, DisplayName: a.DisplayName, Email: a.Email, State: userState(a.State),
		GroupIds: a.GroupIDs, RoleIds: a.RoleIDs, CreatedAt: timestamp(a.CreatedAt),
	}
}

func adminAccounts(list []store.AccountView) []*iamv1.AdminAccount {
	out := make([]*iamv1.AdminAccount, 0, len(list))
	for _, a := range list {
		out = append(out, adminAccount(a))
	}
	return out
}

func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
