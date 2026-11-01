package manage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const PageSize = 100

var (
	ErrAccountID = fmt.Errorf("%w: account id must be 1 to %d bytes, each a letter, digit, underscore or hyphen", apperr.ErrInvalidArgument, ids.MaxIDLen)
	ErrNoChange  = fmt.Errorf("%w: set at least one field", apperr.ErrInvalidArgument)
)

type Deps struct {
	Store store.AdminRunner
	Now   func() time.Time
}

type Service struct {
	deps Deps
}

func New(deps Deps) *Service {
	return &Service{deps: deps}
}

type step struct {
	tx     store.AdminTx
	caller auth.Caller
	grant  authz.Grant
	now    time.Time
}

func (s *Service) write(ctx context.Context, c auth.Caller, rpc string, treeLock bool, fn func(*step) error) error {
	return s.run(ctx, c, rpc, store.AdminTxOptions{Tenant: c.Tenant, TreeLock: treeLock}, fn)
}

func (s *Service) read(ctx context.Context, c auth.Caller, rpc string, fn func(*step) error) error {
	return s.run(ctx, c, rpc, store.AdminTxOptions{Tenant: c.Tenant, ReadOnly: true}, fn)
}

func (s *Service) run(ctx context.Context, c auth.Caller, rpc string, opts store.AdminTxOptions, fn func(*step) error) error {
	now := s.deps.Now()
	return s.deps.Store.RunAdmin(ctx, opts, func(tx store.AdminTx) error {
		st, err := enter(ctx, tx, c, rpc, now)
		if err != nil {
			return err
		}
		return fn(st)
	})
}

func enter(ctx context.Context, tx store.AdminTx, c auth.Caller, rpc string, now time.Time) (*step, error) {
	err := tx.RunningSession(ctx, store.SessionCheck{TenantID: c.Tenant, AccountID: c.Account, SessionID: c.Session, Now: now})
	if errors.Is(err, apperr.ErrNotFound) {
		return nil, auth.ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	g, err := tx.AccountGrant(ctx, c.Tenant, c.Account)
	if err != nil {
		return nil, err
	}
	grant := toGrant(g)
	if err := authz.Require(grant, rpc); err != nil {
		return nil, err
	}
	return &step{tx: tx, caller: c, grant: grant, now: now}, nil
}

func toGrant(g store.Grant) authz.Grant {
	return authz.NewGrant(g.TenantAdmin, toPermissions(g.Permissions))
}

func (st *step) accountGrant(ctx context.Context, account string) (authz.Grant, error) {
	g, err := st.tx.AccountGrant(ctx, st.caller.Tenant, account)
	return toGrant(g), err
}

func (st *step) groupsGrant(ctx context.Context, groupIDs ...string) (authz.Grant, error) {
	g, err := st.tx.GroupsGrant(ctx, st.caller.Tenant, groupIDs)
	return toGrant(g), err
}

func (st *step) roleGrant(ctx context.Context, roleID string) (authz.Grant, error) {
	g, err := st.tx.RoleGrant(ctx, st.caller.Tenant, roleID)
	return toGrant(g), err
}

func (st *step) audit(ctx context.Context, action, account string, detail map[string]string) error {
	return st.tx.Audit(ctx, store.AuditEvent{
		TenantID: st.caller.Tenant, At: st.now, Actor: st.caller.Account, Action: action,
		AccountID: account, SessionID: st.caller.Session, IP: st.caller.IP, Detail: detail,
	})
}

func validAccountID(id string) error {
	if ids.ValidUser(id) != nil {
		return ErrAccountID
	}
	return nil
}

func validPageToken(token string, valid func(string) error) error {
	if token == "" {
		return nil
	}
	if valid(token) != nil {
		return fmt.Errorf("%w: page token", apperr.ErrInvalidArgument)
	}
	return nil
}

func validGroupIDs(list []string) error {
	seen := make(map[string]bool, len(list))
	for _, id := range list {
		if err := directory.ValidateGroupID(id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("%w: group %s listed twice", apperr.ErrInvalidArgument, id)
		}
		seen[id] = true
	}
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
