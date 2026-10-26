package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type RefreshRequest struct {
	Tenant       string
	RefreshToken string
	ClientID     string
	IP           string
	Proof        Proof
}

type RevokeRequest struct {
	Tenant string
	Token  string
	IP     string
}

func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (Tokens, error) {
	sid, ok := session.ParseRefreshToken(req.RefreshToken)
	if !ok {
		s.deps.Stats.RefreshUnknown.Add(1)
		return Tokens{}, ErrInvalidGrant
	}
	sess, err := s.deps.Store.SessionByID(ctx, sid)
	if isNotFound(err) || (err == nil && sess.TenantID != req.Tenant) {
		s.deps.Stats.RefreshUnknown.Add(1)
		return Tokens{}, ErrInvalidGrant
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return Tokens{}, err
	}
	state := sessionState(sess)
	if session.SeenJTI(state, req.Proof.JTI) {
		s.deps.Stats.RefreshReplay.Add(1)
		return Tokens{}, ErrProofReplayed
	}
	now := s.deps.Now()
	outcome := session.Decide(state, session.HashToken(req.RefreshToken), req.Proof.Thumbprint, now, s.cfg.RefreshGrace)
	switch outcome {
	case session.Rotate, session.RotateAgain:
	case session.Reuse:
		s.deps.Stats.RefreshReuse.Add(1)
		if err := s.revoke(ctx, sess, "refresh_reuse", req.IP); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, ErrInvalidGrant
	default:
		s.countRefresh(outcome)
		return Tokens{}, ErrInvalidGrant
	}
	active, err := s.accountActive(ctx, sess.TenantID, sess.AccountID)
	if err != nil {
		s.deps.Stats.RefreshUnavailable.Add(1)
		return Tokens{}, err
	}
	if !active {
		s.deps.Stats.RefreshRevoked.Add(1)
		return Tokens{}, ErrInvalidGrant
	}
	next, err := session.NewRefreshToken(sid, s.deps.Random)
	if err != nil {
		return Tokens{}, fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	advanced := session.Advance(state, session.HashToken(next), req.Proof.JTI, now, s.cfg.SessionIdle)
	rotation := store.Rotation{
		SessionID: sid, ExpectVer: sess.RefreshVer, RefreshHash: advanced.Current, History: advanced.History,
		RecentJTI: advanced.RecentJTI, IdleExpiresAt: advanced.IdleExpiresAt, LastUsedAt: now,
	}
	detail := map[string]string{"outcome": outcome.String()}
	if req.ClientID != "" {
		detail["client_id"] = req.ClientID
	}
	audit := store.AuditEvent{TenantID: sess.TenantID, At: now, Actor: sess.AccountID, Action: "refresh", AccountID: sess.AccountID, SessionID: sid, IP: req.IP, Detail: detail}
	if err := s.deps.Store.RotateRefresh(ctx, rotation, audit); err != nil {
		s.deps.Stats.RefreshUnavailable.Add(1)
		return Tokens{}, err
	}
	access, expires, err := s.signAccess(sess.TenantID, sess.AccountID, sid, now)
	if err != nil {
		return Tokens{}, err
	}
	s.countRefresh(outcome)
	return Tokens{AccessToken: access, ExpiresAt: expires, RefreshToken: next, SessionID: sid}, nil
}

func (s *Service) Revoke(ctx context.Context, req RevokeRequest) error {
	sid, ok := session.ParseRefreshToken(req.Token)
	if !ok {
		if strings.Count(req.Token, ".") == 2 {
			return ErrUnsupportedTokenType
		}
		return nil
	}
	sess, err := s.deps.Store.SessionByID(ctx, sid)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return err
	}
	if sess.TenantID != req.Tenant || sess.State != store.StateActive || !session.Matches(sessionState(sess), session.HashToken(req.Token)) {
		return nil
	}
	return s.revoke(ctx, sess, "logout", req.IP)
}

func (s *Service) revoke(ctx context.Context, sess store.Session, reason, ip string) error {
	now := s.deps.Now()
	r := store.Revocation{By: sess.AccountID, Reason: reason, At: now}
	audit := store.AuditEvent{TenantID: sess.TenantID, At: now, Actor: sess.AccountID, Action: "session_revoke", AccountID: sess.AccountID, SessionID: sess.ID, IP: ip, Detail: map[string]string{"reason": reason}}
	revoked, err := s.deps.Store.RevokeSession(ctx, sess.ID, r, audit)
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return err
	}
	if revoked {
		s.deps.Stats.SessionsRevoked.Add(1)
	}
	return nil
}

func (s *Service) accountActive(ctx context.Context, tenant, account string) (bool, error) {
	t, err := s.deps.Store.TenantByID(ctx, tenant)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return false, err
	}
	a, err := s.deps.Store.AccountByID(ctx, tenant, account)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		s.deps.Stats.StoreErrors.Add(1)
		return false, err
	}
	return t.State == store.StateActive && a.State == store.StateActive, nil
}

func (s *Service) countRefresh(o session.Outcome) {
	st := s.deps.Stats
	switch o {
	case session.Rotate:
		st.RefreshOK.Add(1)
	case session.RotateAgain:
		st.RefreshGrace.Add(1)
	case session.Unknown:
		st.RefreshUnknown.Add(1)
	case session.KeyMismatch:
		st.RefreshKeyMismatch.Add(1)
	case session.Expired:
		st.RefreshExpired.Add(1)
	case session.Revoked:
		st.RefreshRevoked.Add(1)
	case session.Reuse:
		st.RefreshReuse.Add(1)
	}
}

func sessionState(sess store.Session) session.State {
	return session.State{
		Revoked:       sess.State != store.StateActive,
		IdleExpiresAt: sess.IdleExpiresAt,
		ExpiresAt:     sess.ExpiresAt,
		KeyThumbprint: sess.KeyThumbprint,
		Current:       sess.RefreshHash,
		History:       sess.History,
		RecentJTI:     sess.RecentJTI,
	}
}
