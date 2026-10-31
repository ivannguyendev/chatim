package auth_test

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type profileCalls struct {
	err     error
	checks  []store.SessionCheck
	updates []store.ProfileUpdate
	audits  []store.AuditEvent
}

func (f *fakeStore) runningProfile(c store.SessionCheck) (store.Profile, error) {
	f.profile.checks = append(f.profile.checks, c)
	if f.profile.err != nil {
		return store.Profile{}, f.profile.err
	}
	s, ok := f.sessions[c.SessionID]
	if !ok || s.State != store.StateActive || s.TenantID != c.TenantID || s.AccountID != c.AccountID ||
		!s.IdleExpiresAt.After(c.Now) || !s.ExpiresAt.After(c.Now) {
		return store.Profile{}, apperr.ErrNotFound
	}
	return f.profiles[accountKey(c.TenantID, c.AccountID)], nil
}

func (f *fakeStore) MyProfile(_ context.Context, c store.SessionCheck) (store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runningProfile(c)
}

func (f *fakeStore) UpdateMyProfile(_ context.Context, c store.SessionCheck, u store.ProfileUpdate, audit store.AuditEvent) (store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profile.updates = append(f.profile.updates, u)
	p, err := f.runningProfile(c)
	if err != nil {
		return store.Profile{}, err
	}
	if u.DisplayName != nil {
		p.DisplayName = *u.DisplayName
	}
	if u.AvatarURL != nil {
		p.AvatarURL = *u.AvatarURL
	}
	f.profiles[accountKey(c.TenantID, c.AccountID)] = p
	f.profile.audits = append(f.profile.audits, audit)
	return p, nil
}
