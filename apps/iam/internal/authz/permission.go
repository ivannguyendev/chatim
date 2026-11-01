package authz

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Permission string

const (
	IAMClient       = "iam"
	TenantAdminRole = "tenant-admin"

	MaxPermissionBytes = 128
	MaxRoleIDBytes     = 64
)

var IAMPermissions = []string{
	"iam:roles.read",
	"iam:roles.write",
	"iam:roles.assign",
	"iam:groups.read",
	"iam:groups.write",
	"iam:groups.members.write",
	"iam:accounts.read",
	"iam:accounts.create",
	"iam:accounts.update",
	"iam:accounts.reset_password",
	"iam:accounts.sessions.revoke",
	"iam:machines.write",
}

var ErrRoleID = fmt.Errorf("%w: role id must be 1 to %d bytes, each a lowercase letter, digit, underscore or hyphen", apperr.ErrInvalidArgument, MaxRoleIDBytes)

var (
	permissionShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}:[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	roleIDShape     = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
)

func Parse(s string) (Permission, error) {
	if len(s) > MaxPermissionBytes || !permissionShape.MatchString(s) {
		return "", fmt.Errorf("%w: permission %q must look like client:resource.action", apperr.ErrInvalidArgument, s)
	}
	return Permission(s), nil
}

func ParseAll(list []string) ([]Permission, error) {
	out := make([]Permission, 0, len(list))
	for _, s := range list {
		p, err := Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	slices.Sort(out)
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, fmt.Errorf("%w: permission %s listed twice", apperr.ErrInvalidArgument, out[i])
		}
	}
	return out, nil
}

func ValidateRoleID(id string) error {
	if !roleIDShape.MatchString(id) {
		return ErrRoleID
	}
	return nil
}
