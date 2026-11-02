package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	identityv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1/identityv1connect"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const (
	directoryBodyMax = 64 << 10
	maxDirectoryIDs  = 500
	directoryPrefix  = "/" + identityv1connect.DirectoryServiceName + "/"
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

func (d directoryService) GetUsers(_ context.Context, req *connect.Request[identityv1.GetUsersRequest]) (resp *connect.Response[identityv1.GetUsersResponse], err error) {
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
	out := make([]*identityv1.DirectoryUser, 0, len(found))
	for _, u := range found {
		out = append(out, &identityv1.DirectoryUser{Id: u.ID, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL, State: userState(u.State)})
	}
	return connect.NewResponse(&identityv1.GetUsersResponse{Users: out}), nil
}

func (d directoryService) GetMyProfile(_ context.Context, req *connect.Request[identityv1.GetMyProfileRequest]) (*connect.Response[identityv1.GetMyProfileResponse], error) {
	claims, done, err := d.s.enter(req, d.s.directory)
	if err != nil {
		return nil, err
	}
	defer done()
	if !d.s.opts.Directory.Ready() {
		return nil, errUnavailable("directory is loading")
	}
	found := d.s.opts.Directory.Lookup(claims.Tenant, []string{claims.User})
	if len(found) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	u := found[0]
	return connect.NewResponse(&identityv1.GetMyProfileResponse{Profile: &identityv1.MyProfile{Id: u.ID, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL}}), nil
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

func userState(s store.State) identityv1.UserState {
	switch s {
	case store.StateActive:
		return identityv1.UserState_USER_STATE_ACTIVE
	case store.StateDisabled:
		return identityv1.UserState_USER_STATE_DISABLED
	default:
		return identityv1.UserState_USER_STATE_UNSPECIFIED
	}
}

func errUnavailable(msg string) *connect.Error {
	ce := connect.NewError(connect.CodeUnavailable, errors.New(msg))
	ce.Meta().Set("Retry-After", "1")
	return ce
}
