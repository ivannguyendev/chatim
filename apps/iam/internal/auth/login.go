package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type LoginRequest struct {
	Tenant     string
	Username   string
	Password   string
	DeviceName string
	UserAgent  string
	IP         string
	Proof      Proof
}

type ChangePasswordRequest struct {
	Tenant   string
	Username string
	Current  string
	New      string
	IP       string
}

type checked struct {
	account store.Account
	now     time.Time
}

func (s *Service) Login(ctx context.Context, req LoginRequest) (Tokens, error) {
	c, release, err := s.checkPassword(ctx, req.Tenant, req.Username, req.Password, req.IP)
	if err != nil {
		return Tokens{}, err
	}
	defer release()
	a := c.account
	if a.PasswordMustChange {
		s.deps.Stats.LoginMustChange.Add(1)
		return Tokens{}, ErrPasswordChangeRequired
	}
	sid, err := session.NewID(s.deps.Random)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	refresh, err := session.NewRefreshToken(sid, s.deps.Random)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	rehash := ""
	if s.deps.Hasher.NeedsRehash(a.PasswordHash) {
		if h, err := s.deps.Hasher.Hash(req.Password); err == nil {
			rehash = h
		}
	}
	now := c.now
	sess := store.Session{
		ID: sid, TenantID: a.TenantID, AccountID: a.ID, State: store.StateActive,
		DeviceName: req.DeviceName, UserAgent: req.UserAgent, CreatedAt: now, LastUsedAt: now,
		IdleExpiresAt: minTime(now.Add(s.cfg.SessionIdle), now.Add(s.cfg.SessionMax)), ExpiresAt: now.Add(s.cfg.SessionMax),
		RefreshVer: 1, RefreshHash: session.HashToken(refresh), KeyThumbprint: req.Proof.Thumbprint,
		RecentJTI: []string{req.Proof.JTI},
	}
	audit := store.AuditEvent{TenantID: a.TenantID, At: now, Actor: a.ID, Action: "login_ok", AccountID: a.ID, SessionID: sid, IP: req.IP}
	if err := s.deps.Store.CreateSession(ctx, store.NewSession{Session: sess, AccountVer: a.Ver, Rehash: rehash}, audit); err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return Tokens{}, err
	}
	access, expires, err := s.signAccess(a.TenantID, a.ID, sid, now)
	if err != nil {
		return Tokens{}, err
	}
	s.deps.Stats.LoginOK.Add(1)
	return Tokens{AccessToken: access, ExpiresAt: expires, RefreshToken: refresh, SessionID: sid}, nil
}

func (s *Service) ChangePassword(ctx context.Context, req ChangePasswordRequest) error {
	if err := s.deps.Policy.Check(req.New); err != nil {
		return err
	}
	if req.New == req.Current {
		return fmt.Errorf("%w: new password must differ", apperr.ErrInvalidArgument)
	}
	c, release, err := s.checkPassword(ctx, req.Tenant, req.Username, req.Current, req.IP)
	if err != nil {
		return err
	}
	defer release()
	hash, err := s.deps.Hasher.Hash(req.New)
	if err != nil {
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	a := c.account
	change := store.PasswordChange{
		TenantID: a.TenantID, AccountID: a.ID, ExpectVer: a.Ver, Hash: hash,
		Revocation: store.Revocation{By: a.ID, Reason: "password_change", At: c.now},
	}
	audit := store.AuditEvent{TenantID: a.TenantID, At: c.now, Actor: a.ID, Action: "password_change", AccountID: a.ID, IP: req.IP}
	if err := s.deps.Store.SetPassword(ctx, change, audit); err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return err
	}
	s.deps.Stats.PasswordChanged.Add(1)
	return nil
}

func (s *Service) checkPassword(ctx context.Context, tenant, username, pw, ip string) (checked, func(), error) {
	now := s.deps.Now()
	if !s.deps.IPs.Allow(ip, now) {
		s.deps.Stats.LoginThrottledIP.Add(1)
		return checked{}, nil, errTooManyRequests
	}
	normalized, ok := validIdentity(tenant, username)
	if !ok {
		s.deps.Stats.LoginInvalid.Add(1)
		return checked{}, nil, ErrInvalidCredentials
	}
	release, ok := s.deps.Slots.TryAcquire()
	if !ok {
		s.deps.Stats.LoginBusy.Add(1)
		return checked{}, nil, errBusy
	}
	c, err := s.verify(ctx, tenant, normalized, pw, ip, now)
	if err != nil {
		release()
		return checked{}, nil, err
	}
	return c, release, nil
}

func (s *Service) verify(ctx context.Context, tenant, normalized, pw, ip string, now time.Time) (checked, error) {
	a, eligible, err := s.lookup(ctx, tenant, normalized)
	if err != nil {
		s.deps.Hasher.VerifyDummy(pw)
		s.deps.Stats.StoreErrors.Add(1)
		return checked{}, err
	}
	match := false
	if eligible {
		match, _ = s.deps.Hasher.Verify(a.PasswordHash, pw)
	} else {
		s.deps.Hasher.VerifyDummy(pw)
	}
	throttled := eligible && a.NextLoginAt.After(now)
	if eligible && !match {
		next := func(failed int) time.Time { return s.deps.Backoff.NextLoginAt(failed, now) }
		audit := store.AuditEvent{TenantID: tenant, At: now, Actor: a.ID, Action: "login_failed", AccountID: a.ID, IP: ip}
		if err := s.deps.Store.RecordLoginFailure(ctx, tenant, a.ID, next, audit); err != nil {
			s.deps.Stats.StoreErrors.Add(1)
		}
	}
	switch {
	case throttled:
		s.deps.Stats.LoginThrottledAccount.Add(1)
		return checked{}, ErrInvalidCredentials
	case !eligible || !match:
		s.deps.Stats.LoginInvalid.Add(1)
		return checked{}, ErrInvalidCredentials
	}
	return checked{account: a, now: now}, nil
}

func (s *Service) lookup(ctx context.Context, tenant, username string) (store.Account, bool, error) {
	t, err := s.deps.Store.TenantByID(ctx, tenant)
	if isNotFound(err) {
		return store.Account{}, false, nil
	}
	if err != nil {
		return store.Account{}, false, err
	}
	a, err := s.deps.Store.AccountByUsername(ctx, tenant, username)
	if isNotFound(err) {
		return store.Account{}, false, nil
	}
	if err != nil {
		return store.Account{}, false, err
	}
	return a, t.State == store.StateActive && a.State == store.StateActive, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
