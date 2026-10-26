package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type SessionView struct {
	ID         string
	DeviceName string
	UserAgent  string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Current    bool
}

func (s *Service) ListSessions(ctx context.Context, c Caller) ([]SessionView, error) {
	if err := s.checkCaller(ctx, c); err != nil {
		return nil, err
	}
	sessions, err := s.deps.Store.ActiveSessions(ctx, c.Tenant, c.Account)
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return nil, err
	}
	views := make([]SessionView, 0, len(sessions))
	for _, sess := range sessions {
		views = append(views, SessionView{
			ID: sess.ID, DeviceName: sess.DeviceName, UserAgent: sess.UserAgent,
			CreatedAt: sess.CreatedAt, LastUsedAt: sess.LastUsedAt, Current: sess.ID == c.Session,
		})
	}
	return views, nil
}

func (s *Service) RevokeSession(ctx context.Context, c Caller, id string) error {
	if err := s.checkCaller(ctx, c); err != nil {
		return err
	}
	target, err := s.deps.Store.SessionByID(ctx, id)
	if isNotFound(err) || (err == nil && (target.TenantID != c.Tenant || target.AccountID != c.Account || target.State != store.StateActive)) {
		return fmt.Errorf("%w: session", apperr.ErrNotFound)
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return err
	}
	return s.revoke(ctx, target, "user", c.IP)
}

func (s *Service) checkCaller(ctx context.Context, c Caller) error {
	sess, err := s.deps.Store.SessionByID(ctx, c.Session)
	if isNotFound(err) {
		return ErrSessionInvalid
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return err
	}
	if sess.State != store.StateActive || sess.TenantID != c.Tenant || sess.AccountID != c.Account {
		return ErrSessionInvalid
	}
	return nil
}
