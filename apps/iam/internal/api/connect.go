package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
)

const bearerPrefix = "bearer "

type connectService struct {
	s *server
}

func (c connectService) Login(ctx context.Context, req *connect.Request[iamv1.LoginRequest]) (*connect.Response[iamv1.LoginResponse], error) {
	proofs := req.Header().Values("DPoP")
	if len(proofs) != 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(msgInvalidProof))
	}
	proof, err := c.s.opts.Proofs.Verify(proofs[0], http.MethodPost, iamv1connect.AuthServiceLoginProcedure)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(msgInvalidProof))
	}
	m := req.Msg
	tok, err := c.s.opts.Auth.Login(ctx, auth.LoginRequest{
		Tenant: m.GetTenant(), Username: m.GetUsername(), Password: m.GetPassword(),
		DeviceName: m.GetDeviceName(), UserAgent: req.Header().Get("User-Agent"), IP: c.ip(req),
		Proof: auth.Proof{Thumbprint: proof.Thumbprint, JTI: proof.JTI},
	})
	if err != nil {
		return nil, c.s.connectError(ctx, req.Spec().Procedure, err)
	}
	res := connect.NewResponse(&iamv1.LoginResponse{
		AccessToken: tok.AccessToken, TokenType: "Bearer", ExpiresIn: c.s.expiresIn(tok),
		RefreshToken: tok.RefreshToken, SessionId: tok.SessionID,
	})
	noStore(res.Header())
	return res, nil
}

func (c connectService) ChangePassword(ctx context.Context, req *connect.Request[iamv1.ChangePasswordRequest]) (*connect.Response[iamv1.ChangePasswordResponse], error) {
	m := req.Msg
	err := c.s.opts.Auth.ChangePassword(ctx, auth.ChangePasswordRequest{
		Tenant: m.GetTenant(), Username: m.GetUsername(), Current: m.GetCurrentPassword(), New: m.GetNewPassword(), IP: c.ip(req),
	})
	if err != nil {
		return nil, c.s.connectError(ctx, req.Spec().Procedure, err)
	}
	return connect.NewResponse(&iamv1.ChangePasswordResponse{}), nil
}

func (c connectService) ListSessions(ctx context.Context, req *connect.Request[iamv1.ListSessionsRequest]) (*connect.Response[iamv1.ListSessionsResponse], error) {
	caller, err := c.caller(req)
	if err != nil {
		return nil, err
	}
	views, err := c.s.opts.Auth.ListSessions(ctx, caller)
	if err != nil {
		return nil, c.s.connectError(ctx, req.Spec().Procedure, err)
	}
	out := make([]*iamv1.Session, 0, len(views))
	for _, v := range views {
		out = append(out, &iamv1.Session{
			SessionId: v.ID, DeviceName: v.DeviceName, UserAgent: v.UserAgent,
			CreatedAt: timestamppb.New(v.CreatedAt), LastUsedAt: timestamppb.New(v.LastUsedAt), Current: v.Current,
		})
	}
	return connect.NewResponse(&iamv1.ListSessionsResponse{Sessions: out}), nil
}

func (c connectService) RevokeSession(ctx context.Context, req *connect.Request[iamv1.RevokeSessionRequest]) (*connect.Response[iamv1.RevokeSessionResponse], error) {
	caller, err := c.caller(req)
	if err != nil {
		return nil, err
	}
	if err := c.s.opts.Auth.RevokeSession(ctx, caller, req.Msg.GetSessionId()); err != nil {
		return nil, c.s.connectError(ctx, req.Spec().Procedure, err)
	}
	return connect.NewResponse(&iamv1.RevokeSessionResponse{}), nil
}

type headered interface {
	Header() http.Header
	Peer() connect.Peer
}

func (c connectService) ip(req headered) string {
	return guard.ClientIPFrom(req.Header(), req.Peer().Addr, c.s.opts.ClientIPHeader)
}

func (c connectService) caller(req headered) (auth.Caller, error) {
	values := req.Header().Values("Authorization")
	unauthenticated := connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	if len(values) != 1 || len(values[0]) <= len(bearerPrefix) || !strings.EqualFold(values[0][:len(bearerPrefix)], bearerPrefix) {
		return auth.Caller{}, unauthenticated
	}
	caller, err := c.s.opts.Callers.Caller(values[0][len(bearerPrefix):])
	if err != nil {
		return auth.Caller{}, unauthenticated
	}
	caller.IP = c.ip(req)
	return caller, nil
}

func (s *server) expiresIn(tok auth.Tokens) int64 {
	return int64(tok.ExpiresAt.Sub(s.opts.Now()).Round(time.Second) / time.Second)
}
