package store

import "github.com/ivannguyendev/chatim/modules/mention/internal/domain"

type tenantID struct {
	tenant string
	id     string
}

func ValidateUserGroups(items []domain.UserGroups) error {
	seen := make(map[tenantID]struct{}, len(items))
	for _, it := range items {
		if err := it.Validate(); err != nil {
			return err
		}
		k := tenantID{it.Tenant, it.User}
		if _, dup := seen[k]; dup {
			return invalid("user listed twice")
		}
		seen[k] = struct{}{}
	}
	return nil
}

func ValidateGroups(items []domain.Group) error {
	seen := make(map[tenantID]struct{}, len(items))
	for _, it := range items {
		if err := it.Validate(); err != nil {
			return err
		}
		k := tenantID{it.Tenant, it.ID}
		if _, dup := seen[k]; dup {
			return invalid("group listed twice")
		}
		seen[k] = struct{}{}
	}
	return nil
}
