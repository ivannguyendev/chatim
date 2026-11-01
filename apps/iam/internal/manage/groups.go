package manage

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

type NewGroup struct {
	ID          string
	Name        string
	ParentID    string
	Mentionable *bool
}

func (s *Service) CreateGroup(ctx context.Context, c auth.Caller, in NewGroup) (store.Group, error) {
	name, err := validNewGroup(in)
	if err != nil {
		return store.Group{}, err
	}
	g := store.Group{TenantID: c.Tenant, ID: in.ID, Name: name, ParentID: in.ParentID, Mentionable: true, State: store.GroupLive, UpdatedBy: c.Account}
	if in.Mentionable != nil {
		g.Mentionable = *in.Mentionable
	}
	err = s.write(ctx, c, "CreateGroup", in.ParentID != "", func(st *step) error {
		chain, err := st.parentChain(ctx, in.ParentID)
		if err != nil {
			return err
		}
		if err := authz.CheckGroupCreate(st.grant, chain); err != nil {
			return err
		}
		g.CreatedAt, g.UpdatedAt = st.now, st.now
		if err := st.tx.CreateGroup(ctx, g); err != nil {
			return err
		}
		return st.audit(ctx, "group_create", "", map[string]string{"group": in.ID})
	})
	if err != nil {
		return store.Group{}, err
	}
	return g, nil
}

func validNewGroup(in NewGroup) (string, error) {
	if err := directory.ValidateGroupID(in.ID); err != nil {
		return "", err
	}
	if in.ParentID != "" {
		if err := directory.ValidateGroupID(in.ParentID); err != nil {
			return "", err
		}
	}
	return directory.NormalizeGroupName(in.Name)
}

func (st *step) parentChain(ctx context.Context, parentID string) (authz.Grant, error) {
	if parentID == "" {
		return authz.Grant{}, nil
	}
	if _, err := st.tx.GroupNode(ctx, st.caller.Tenant, parentID); err != nil {
		return authz.Grant{}, err
	}
	return st.groupsGrant(ctx, parentID)
}

func (s *Service) UpdateGroup(ctx context.Context, c auth.Caller, id string, u store.GroupUpdate) (store.Group, error) {
	update, err := validGroupUpdate(id, u)
	if err != nil {
		return store.Group{}, err
	}
	var g store.Group
	err = s.write(ctx, c, "UpdateGroup", update.ParentID != nil, func(st *step) error {
		node, err := st.tx.GroupNode(ctx, c.Tenant, id)
		if err != nil {
			return err
		}
		if update.ParentID != nil {
			if err := st.checkMove(ctx, id, *update.ParentID); err != nil {
				return err
			}
		}
		if err := st.tx.UpdateGroup(ctx, c.Tenant, id, update, st.now, c.Account); err != nil {
			return err
		}
		g = applyGroupUpdate(node.Group, update, st.now, c.Account)
		return st.audit(ctx, "group_update", "", map[string]string{"group": id})
	})
	if err != nil {
		return store.Group{}, err
	}
	return g, nil
}

func validGroupUpdate(id string, u store.GroupUpdate) (store.GroupUpdate, error) {
	if err := directory.ValidateGroupID(id); err != nil {
		return store.GroupUpdate{}, err
	}
	if u.Name == nil && u.ParentID == nil && u.Mentionable == nil {
		return store.GroupUpdate{}, ErrNoChange
	}
	out := store.GroupUpdate{ParentID: u.ParentID, Mentionable: u.Mentionable}
	if u.Name != nil {
		name, err := directory.NormalizeGroupName(*u.Name)
		if err != nil {
			return store.GroupUpdate{}, err
		}
		out.Name = &name
	}
	if u.ParentID != nil && *u.ParentID != "" {
		if err := directory.ValidateGroupID(*u.ParentID); err != nil {
			return store.GroupUpdate{}, err
		}
	}
	return out, nil
}

func (st *step) checkMove(ctx context.Context, id, parentID string) error {
	oldChain, err := st.groupsGrant(ctx, id)
	if err != nil {
		return err
	}
	newChain, err := st.parentChain(ctx, parentID)
	if err != nil {
		return err
	}
	return authz.CheckGroupMove(st.grant, oldChain, newChain)
}

func applyGroupUpdate(g store.Group, u store.GroupUpdate, at time.Time, by string) store.Group {
	if u.Name != nil {
		g.Name = *u.Name
	}
	if u.ParentID != nil {
		g.ParentID = *u.ParentID
	}
	if u.Mentionable != nil {
		g.Mentionable = *u.Mentionable
	}
	g.UpdatedAt, g.UpdatedBy = at, by
	return g
}

func (s *Service) DeleteGroup(ctx context.Context, c auth.Caller, id string) error {
	if err := directory.ValidateGroupID(id); err != nil {
		return err
	}
	return s.write(ctx, c, "DeleteGroup", true, func(st *step) error {
		if _, err := st.tx.GroupNode(ctx, c.Tenant, id); err != nil {
			return err
		}
		chain, err := st.groupsGrant(ctx, id)
		if err != nil {
			return err
		}
		if err := authz.CheckGroupDelete(st.grant, chain); err != nil {
			return err
		}
		roles, err := st.tx.DeleteGroup(ctx, c.Tenant, id, st.now, c.Account)
		if err != nil {
			return err
		}
		return st.audit(ctx, "group_delete", "", map[string]string{"group": id, "roles": strconv.FormatInt(roles, 10)})
	})
}

func (s *Service) ListGroups(ctx context.Context, c auth.Caller, pageToken string) ([]store.GroupView, string, error) {
	if err := validPageToken(pageToken, directory.ValidateGroupID); err != nil {
		return nil, "", err
	}
	var (
		page []store.GroupView
		next string
	)
	err := s.read(ctx, c, "ListGroups", func(st *step) error {
		var err error
		page, next, err = st.tx.Groups(ctx, c.Tenant, pageToken, PageSize)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}

func (s *Service) SetAccountGroups(ctx context.Context, c auth.Caller, accountID string, groupIDs []string) error {
	if err := firstError(validAccountID(accountID), validGroupIDs(groupIDs)); err != nil {
		return err
	}
	return s.write(ctx, c, "SetAccountGroups", false, func(st *step) error {
		if err := st.tx.LockAccount(ctx, c.Tenant, accountID); err != nil {
			return err
		}
		target, err := st.accountGrant(ctx, accountID)
		if err != nil {
			return err
		}
		if err := st.tx.SetAccountGroups(ctx, c.Tenant, accountID, groupIDs); err != nil {
			return err
		}
		joined, err := st.groupsGrant(ctx, groupIDs...)
		if err != nil {
			return err
		}
		if err := authz.CheckAccountGroups(st.grant, target, joined); err != nil {
			return err
		}
		return st.audit(ctx, "account_groups_set", accountID, map[string]string{"groups": strings.Join(groupIDs, ",")})
	})
}
