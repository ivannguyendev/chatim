package domain

import (
	"slices"
	"strings"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type UserGroups struct {
	Tenant string
	User   string
	Groups []string
	Ver    uint64
}

func (u UserGroups) Validate() error {
	if err := ids.ValidTenant(u.Tenant); err != nil {
		return err
	}
	if err := ids.ValidUser(u.User); err != nil {
		return err
	}
	if u.Ver == 0 {
		return invalid("user groups ver")
	}
	for _, g := range u.Groups {
		if !keys.ValidMentionID(g) {
			return invalid("group id")
		}
	}
	return nil
}

type Group struct {
	Tenant string
	ID     string
	Name   string
	Active bool
	Ver    uint64
}

func (g Group) Validate() error {
	if err := ids.ValidTenant(g.Tenant); err != nil {
		return err
	}
	switch {
	case !keys.ValidMentionID(g.ID):
		return invalid("group id")
	case strings.TrimSpace(g.Name) == "":
		return invalid("group name")
	case g.Ver == 0:
		return invalid("group ver")
	default:
		return nil
	}
}

func NormalizeGroups(groups []string) []string {
	out := make([]string, len(groups))
	copy(out, groups)
	slices.Sort(out)
	return slices.Compact(out)
}
