package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errEmptyProfileUpdate = fmt.Errorf("%w: set a display name or an avatar url", apperr.ErrInvalidArgument)

func (s *Service) MyProfile(ctx context.Context, c Caller) (store.Profile, error) {
	p, err := s.deps.Store.MyProfile(ctx, s.sessionCheck(c))
	if err != nil {
		return store.Profile{}, s.profileError(err)
	}
	return p, nil
}

func (s *Service) UpdateMyProfile(ctx context.Context, c Caller, u store.ProfileUpdate) (store.Profile, error) {
	clean, fields, err := cleanProfileUpdate(u)
	if err != nil {
		return store.Profile{}, err
	}
	check := s.sessionCheck(c)
	audit := store.AuditEvent{
		TenantID: c.Tenant, At: check.Now, Actor: c.Account, Action: "profile_update", AccountID: c.Account,
		SessionID: c.Session, IP: c.IP, Detail: map[string]string{"fields": fields},
	}
	p, err := s.deps.Store.UpdateMyProfile(ctx, check, clean, audit)
	if err != nil {
		return store.Profile{}, s.profileError(err)
	}
	return p, nil
}

func (s *Service) sessionCheck(c Caller) store.SessionCheck {
	return store.SessionCheck{TenantID: c.Tenant, AccountID: c.Account, SessionID: c.Session, Now: s.deps.Now()}
}

func (s *Service) profileError(err error) error {
	if isNotFound(err) {
		return ErrSessionInvalid
	}
	s.deps.Stats.StoreErrors.Add(1)
	return err
}

func cleanProfileUpdate(u store.ProfileUpdate) (store.ProfileUpdate, string, error) {
	var clean store.ProfileUpdate
	var fields []string
	if u.DisplayName != nil {
		name, err := directory.NormalizeDisplayName(*u.DisplayName)
		if err != nil {
			return store.ProfileUpdate{}, "", err
		}
		clean.DisplayName = &name
		fields = append(fields, "display_name")
	}
	if u.AvatarURL != nil {
		if err := directory.ValidateAvatarURL(*u.AvatarURL); err != nil {
			return store.ProfileUpdate{}, "", err
		}
		avatar := *u.AvatarURL
		clean.AvatarURL = &avatar
		fields = append(fields, "avatar_url")
	}
	if len(fields) == 0 {
		return store.ProfileUpdate{}, "", errEmptyProfileUpdate
	}
	return clean, strings.Join(fields, ","), nil
}
