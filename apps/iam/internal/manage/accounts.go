package manage

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	groupsRead authz.Permission = "iam:groups.read"
	rolesRead  authz.Permission = "iam:roles.read"
)

func (s *Service) GetAccount(ctx context.Context, c auth.Caller, id string) (store.AccountView, error) {
	if err := validAccountID(id); err != nil {
		return store.AccountView{}, err
	}
	var view store.AccountView
	err := s.read(ctx, c, "GetAccount", func(st *step) error {
		v, err := st.tx.Account(ctx, c.Tenant, id)
		if err != nil {
			return err
		}
		view = visibleTo(st.grant, v)
		return nil
	})
	if err != nil {
		return store.AccountView{}, err
	}
	return view, nil
}

func (s *Service) ListAccounts(ctx context.Context, c auth.Caller, pageToken string) ([]store.AccountView, string, error) {
	if err := validPageToken(pageToken, validAccountID); err != nil {
		return nil, "", err
	}
	var (
		page []store.AccountView
		next string
	)
	err := s.read(ctx, c, "ListAccounts", func(st *step) error {
		views, last, err := st.tx.Accounts(ctx, c.Tenant, pageToken, PageSize)
		if err != nil {
			return err
		}
		page = make([]store.AccountView, 0, len(views))
		for _, v := range views {
			page = append(page, visibleTo(st.grant, v))
		}
		next = last
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}

func visibleTo(caller authz.Grant, v store.AccountView) store.AccountView {
	if !caller.Has(groupsRead) || v.GroupIDs == nil {
		v.GroupIDs = []string{}
	}
	if !caller.Has(rolesRead) || v.RoleIDs == nil {
		v.RoleIDs = []string{}
	}
	return v
}
