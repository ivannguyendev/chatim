package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const (
	directoryBodyMax = 64 << 10
	maxDirectoryIDs  = 500
	directoryPrefix  = "/" + iamv1connect.DirectoryServiceName + "/"
)

type Directory interface {
	Lookup(tenant string, ids []string) []directory.User
	Ready() bool
}

type Revocations interface {
	Check(c jwtverify.Claims) revocation.Verdict
}

type Claims interface {
	Claims(token string) (jwtverify.Claims, error)
}

type directoryService struct {
	s *server
}

func (s *server) bodyLimit(path string) int64 {
	if s.opts.Directory != nil && strings.HasPrefix(path, directoryPrefix) {
		return directoryBodyMax
	}
	return s.opts.BodyMax
}

func (d directoryService) GetUsers(_ context.Context, req *connect.Request[iamv1.GetUsersRequest]) (resp *connect.Response[iamv1.GetUsersResponse], err error) {
	defer func() { d.s.opts.DirectoryStats.countGetUsers(err) }()
	claims, done, err := d.s.enter(req, d.s.directory)
	if err != nil {
		return nil, err
	}
	defer done()
	wanted, err := uniqueUserIDs(req.Msg.GetUserIds())
	if err != nil {
		return nil, err
	}
	if !d.s.opts.Directory.Ready() {
		return nil, errUnavailable("directory is loading")
	}
	d.s.opts.DirectoryStats.GetUsersIDs.Add(uint64(len(wanted)))
	found := d.s.opts.Directory.Lookup(claims.Tenant, wanted)
	out := make([]*iamv1.DirectoryUser, 0, len(found))
	for _, u := range found {
		out = append(out, &iamv1.DirectoryUser{Id: u.ID, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL, State: userState(u.State)})
	}
	return connect.NewResponse(&iamv1.GetUsersResponse{Users: out}), nil
}

func (d directoryService) GetMyProfile(ctx context.Context, req *connect.Request[iamv1.GetMyProfileRequest]) (*connect.Response[iamv1.GetMyProfileResponse], error) {
	claims, done, err := d.s.enter(req, d.s.directory)
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := d.s.opts.Auth.MyProfile(ctx, d.s.callerOf(req, claims))
	if err != nil {
		return nil, d.s.connectError(ctx, req.Spec().Procedure, err)
	}
	return connect.NewResponse(&iamv1.GetMyProfileResponse{Profile: myProfile(p)}), nil
}

func (d directoryService) UpdateMyProfile(ctx context.Context, req *connect.Request[iamv1.UpdateMyProfileRequest]) (*connect.Response[iamv1.UpdateMyProfileResponse], error) {
	claims, done, err := d.s.enter(req, d.s.directory)
	if err != nil {
		return nil, err
	}
	defer done()
	update := store.ProfileUpdate{DisplayName: req.Msg.DisplayName, AvatarURL: req.Msg.AvatarUrl}
	p, err := d.s.opts.Auth.UpdateMyProfile(ctx, d.s.callerOf(req, claims), update)
	if err != nil {
		return nil, d.s.connectError(ctx, req.Spec().Procedure, err)
	}
	return connect.NewResponse(&iamv1.UpdateMyProfileResponse{Profile: myProfile(p)}), nil
}

type limiter struct {
	gate             *userlimit.Gate
	users            *userlimit.Users
	userLimited      *atomic.Uint64
	inflightRejected *atomic.Uint64
}

func (s *server) enter(req headered, l limiter) (jwtverify.Claims, func(), error) {
	if !l.gate.Enter() {
		l.inflightRejected.Add(1)
		return jwtverify.Claims{}, nil, errUnavailable("temporarily unavailable")
	}
	claims, err := s.authenticate(req)
	if err != nil {
		l.gate.Leave()
		return jwtverify.Claims{}, nil, err
	}
	release, err := l.users.Acquire(userlimit.Key{Tenant: claims.Tenant, User: claims.User})
	if err != nil {
		l.gate.Leave()
		l.userLimited.Add(1)
		return jwtverify.Claims{}, nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many requests, retry later"))
	}
	return claims, func() {
		release()
		l.gate.Leave()
	}, nil
}

func (s *server) authenticate(req headered) (jwtverify.Claims, error) {
	token, ok := bearerToken(req.Header())
	if !ok {
		return jwtverify.Claims{}, errUnauthenticated()
	}
	claims, err := s.opts.Claims.Claims(token)
	if err != nil {
		return jwtverify.Claims{}, errUnauthenticated()
	}
	if s.opts.Revocations.Check(claims) != revocation.Allowed {
		return jwtverify.Claims{}, connect.NewError(connect.CodeUnauthenticated, errors.New("session revoked"))
	}
	return claims, nil
}

func (s *server) callerOf(req headered, claims jwtverify.Claims) auth.Caller {
	return auth.Caller{Tenant: claims.Tenant, Account: claims.User, Session: claims.Session, IP: s.clientIP(req)}
}

func uniqueUserIDs(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > maxDirectoryIDs {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("user_ids must hold 1 to %d ids", maxDirectoryIDs))
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		if ids.ValidUser(id) != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_ids holds an invalid user id"))
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func userState(s store.State) iamv1.UserState {
	switch s {
	case store.StateActive:
		return iamv1.UserState_USER_STATE_ACTIVE
	case store.StateDisabled:
		return iamv1.UserState_USER_STATE_DISABLED
	default:
		return iamv1.UserState_USER_STATE_UNSPECIFIED
	}
}

func myProfile(p store.Profile) *iamv1.MyProfile {
	return &iamv1.MyProfile{Id: p.ID, Username: p.Username, Email: p.Email, DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL}
}

func errUnavailable(msg string) *connect.Error {
	ce := connect.NewError(connect.CodeUnavailable, errors.New(msg))
	ce.Meta().Set("Retry-After", "1")
	return ce
}
