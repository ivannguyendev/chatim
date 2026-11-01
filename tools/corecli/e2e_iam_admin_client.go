package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"connectrpc.com/connect"

	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
)

var accessTokenClaims = []string{"aud", "exp", "iat", "iss", "sid", "sub", "tid"}

type adminClient struct {
	rpc    iamv1connect.AdminServiceClient
	access string
}

func newAdminClient(c *iamClient, access string) *adminClient {
	return &adminClient{rpc: iamv1connect.NewAdminServiceClient(c.http, c.base), access: access}
}

func adminRequest[T any](a *adminClient, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+a.access)
	return req
}

func (a *adminClient) createRole(ctx context.Context, id string, permissions ...string) error {
	_, err := a.rpc.CreateRole(ctx, adminRequest(a, &iamv1.CreateRoleRequest{Id: id, Name: id, Permissions: permissions}))
	return err
}

func (a *adminClient) assignGroupRole(ctx context.Context, group, role string) error {
	_, err := a.rpc.AssignGroupRole(ctx, adminRequest(a, &iamv1.AssignGroupRoleRequest{GroupId: group, RoleId: role}))
	return err
}

func (a *adminClient) unassignGroupRole(ctx context.Context, group, role string) error {
	_, err := a.rpc.UnassignGroupRole(ctx, adminRequest(a, &iamv1.UnassignGroupRoleRequest{GroupId: group, RoleId: role}))
	return err
}

func (a *adminClient) setAccountGroups(ctx context.Context, account string, groups ...string) error {
	_, err := a.rpc.SetAccountGroups(ctx, adminRequest(a, &iamv1.SetAccountGroupsRequest{AccountId: account, GroupIds: groups}))
	return err
}

func expectCode(err error, want connect.Code, what string) error {
	if err == nil {
		return fmt.Errorf("%s succeeded, want %s", what, want)
	}
	if got := connect.CodeOf(err); got != want {
		return fmt.Errorf("%s = %w, want %s", what, err, want)
	}
	return nil
}

func checkAccessTokenClaims(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("the access token is not a compact JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode the access token payload: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(raw, &claims); err != nil {
		return fmt.Errorf("parse the access token payload: %w", err)
	}
	keys := slices.Sorted(maps.Keys(claims))
	if !slices.Equal(keys, accessTokenClaims) {
		return fmt.Errorf("access token claims = %v, want exactly %v", keys, accessTokenClaims)
	}
	return nil
}
